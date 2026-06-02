package server

// server_cluster.go — fork-owned. All multi-node cluster-sync server methods live here so
// the upstream server.go keeps only the documented hook patches (see FORK_PATCHES.md).
//
// These are methods on *BifrostHTTPServer; Go allows a type's methods to span files in the
// same package, so this file reaches unexported server state with zero upstream merge conflict.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/plugins/governance"
)

// getGovernanceLocalStore returns the governance *LocalGovernanceStore or nil.
func (s *BifrostHTTPServer) getGovernanceLocalStore() *governance.LocalGovernanceStore {
	if s.Config == nil || !s.Config.IsPluginLoaded(s.getGovernancePluginName()) {
		return nil
	}
	gp, err := s.getGovernancePlugin()
	if err != nil {
		return nil
	}
	store := gp.GetGovernanceStore()
	ls, ok := store.(*governance.LocalGovernanceStore)
	if !ok {
		return nil
	}
	return ls
}

// initClusterPublishing wraps ConfigStore with PublishingConfigStore when cluster.redis is configured.
// Must run before LoadPlugins so governance and all handlers use the publishing store.
func (s *BifrostHTTPServer) initClusterPublishing(ctx context.Context) error {
	if s.Config == nil || s.Config.Cluster == nil || s.Config.ConfigStore == nil {
		return nil
	}
	client, err := s.Config.Cluster.Redis.NewRedisUniversalClient()
	if err != nil {
		return fmt.Errorf("cluster redis client: %w", err)
	}
	if client == nil {
		return nil
	}
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Warn("cluster redis unavailable at startup, multi-node sync disabled: %v", err)
		_ = client.Close()
		return nil
	}
	s.clusterRedisClient = client
	s.clusterSyncer = configstore.NewRedisClusterSyncer(client, logger)
	s.clusterEventNodeID = uuid.New().String()
	s.Config.ConfigStore = configstore.NewPublishingConfigStore(
		s.Config.ConfigStore,
		s.clusterSyncer,
		s.clusterEventNodeID,
		logger,
	)
	logger.Info("cluster: publishing enabled (event node id %s)", s.clusterEventNodeID)
	return nil
}

// initClusterSubscriberAndRedis runs Redis counter recovery and starts the config stream consumer.
// Must run after Bifrost client and routes exist (FullReload needs Client).
func (s *BifrostHTTPServer) initClusterSubscriberAndRedis(ctx context.Context) error {
	if s.clusterSyncer == nil || s.clusterRedisClient == nil || s.Config == nil || s.Config.Cluster == nil {
		return nil
	}
	clusterCfg := s.Config.Cluster
	if gov := s.getGovernanceLocalStore(); gov != nil {
		if ok := gov.InitRedis(ctx, s.clusterRedisClient); ok {
			gov.SetRedisAvailable(true)
			logger.Info("cluster: Redis recovery merge complete; using Redis for RL/budget counters")
		} else {
			logger.Warn("cluster: Redis recovery merge failed; operating in degraded mode (DB-only counters)")
		}
	}
	consumerID := clusterCfg.ConsumerID()
	s.clusterCtx, s.clusterCancel = context.WithCancel(ctx)
	go s.clusterSyncer.Subscribe(s.clusterCtx, consumerID, s.clusterEventNodeID, s.FullReload, s.handleConfigSyncEvent)
	logger.Info("cluster: subscriber running (consumerID=%s)", consumerID)
	return nil
}
// FullReload reloads runtime state from the DB in a fixed order. DB is authoritative: entities
// present in memory but absent from the DB are removed where governance applies.
func (s *BifrostHTTPServer) FullReload(ctx context.Context) error {
	if s.Config == nil || s.Config.ConfigStore == nil {
		return fmt.Errorf("config store not initialized")
	}
	if s.Client == nil {
		return fmt.Errorf("bifrost client not initialized")
	}

	s.fullReloadMu.Lock()
	defer s.fullReloadMu.Unlock()

	govOK := s.Config.IsPluginLoaded(s.getGovernancePluginName())
	var govData *governance.GovernanceData
	if govOK {
		if gp, err := s.getGovernancePlugin(); err == nil {
			govData = gp.GetGovernanceStore().GetGovernanceData(ctx)
		}
	}

	inMemProviders := s.Config.GetAvailableProviders()

	if err := s.ReloadClientConfigFromConfigStore(ctx); err != nil {
		logger.Warn("FullReload: client config reload failed: %v", err)
	}

	// Auth config — update AuthMiddleware in-memory only (no DB write).
	if authConfig, err := s.Config.ConfigStore.GetAuthConfig(ctx); err == nil && authConfig != nil {
		if s.AuthMiddleware != nil {
			s.AuthMiddleware.UpdateAuthConfig(authConfig)
		}
	} else if err != nil {
		logger.Warn("FullReload: auth config reload failed: %v", err)
	}

	// Proxy config — update s.Config.ProxyConfig in-memory.
	// When the store has no proxy row, GetProxyConfig returns (nil, nil). We must still call
	// ReloadProxyConfig with nil so in-memory proxy is cleared (do not add && proxyConfig != nil).
	if proxyConfig, err := s.Config.ConfigStore.GetProxyConfig(ctx); err == nil {
		_ = s.ReloadProxyConfig(ctx, proxyConfig)
	} else {
		logger.Warn("FullReload: proxy config reload failed: %v", err)
	}

	// Framework config — map DB row into FrameworkConfig.Pricing then call UpdateSyncConfig.
	if dbFwConfig, err := s.Config.ConfigStore.GetFrameworkConfig(ctx); err == nil && dbFwConfig != nil {
		if s.Config.FrameworkConfig == nil {
			s.Config.FrameworkConfig = &framework.FrameworkConfig{}
		}
		s.Config.FrameworkConfig.Pricing = frameworkPricingConfig(dbFwConfig)
		if err := s.UpdateSyncConfig(ctx); err != nil {
			logger.Warn("FullReload: framework config sync failed: %v", err)
		}
	} else if err != nil {
		logger.Warn("FullReload: framework config reload failed: %v", err)
	}

	// Pricing overrides — full reload from DB into ModelCatalog (no remote pricing URL fetch).
	if err := s.ReloadPricingFromDBAndPopulateModelPool(ctx); err != nil {
		logger.Warn("FullReload: pricing overrides reload failed: %v", err)
	}

	providers, err := s.Config.ConfigStore.GetProviders(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to list providers: %v", err)
	} else {
		dbProviderSet := make(map[schemas.ModelProvider]bool)
		for _, p := range providers {
			pr := schemas.ModelProvider(p.Name)
			dbProviderSet[pr] = true
			if _, err := s.ReloadProvider(ctx, pr); err != nil {
				logger.Warn("FullReload: provider %s reload failed: %v", p.Name, err)
			}
		}
		skipCtx := context.WithValue(ctx, schemas.BifrostContextKeySkipDBUpdate, true)
		for _, mp := range inMemProviders {
			if !dbProviderSet[mp] {
				if err := s.RemoveProvider(skipCtx, mp); err != nil {
					logger.Warn("FullReload: RemoveProvider %s failed: %v", mp, err)
				}
			}
		}
	}

	if govOK {
		modelConfigs, err := s.Config.ConfigStore.GetModelConfigs(ctx)
		if err != nil {
			logger.Warn("FullReload: failed to list model configs: %v", err)
		} else {
			dbMCSet := make(map[string]bool)
			for _, mc := range modelConfigs {
				dbMCSet[mc.ID] = true
				if _, err := s.ReloadModelConfig(ctx, mc.ID); err != nil {
					logger.Warn("FullReload: model config %s reload failed: %v", mc.ID, err)
				}
			}
			if govData != nil {
				for _, mc := range govData.ModelConfigs {
					if mc != nil && !dbMCSet[mc.ID] {
						if err := s.RemoveModelConfig(ctx, mc.ID); err != nil {
							logger.Warn("FullReload: RemoveModelConfig %s failed: %v", mc.ID, err)
						}
					}
				}
			}
		}

		virtualKeys, err := s.Config.ConfigStore.GetVirtualKeys(ctx)
		if err != nil {
			logger.Warn("FullReload: failed to list virtual keys: %v", err)
		} else {
			dbVKSet := make(map[string]bool)
			for _, vk := range virtualKeys {
				dbVKSet[vk.ID] = true
				if _, err := s.ReloadVirtualKey(ctx, vk.ID); err != nil {
					logger.Warn("FullReload: virtual key %s reload failed: %v", vk.ID, err)
				}
			}
			if govData != nil {
				for id := range govData.VirtualKeys {
					if !dbVKSet[id] {
						if err := s.RemoveVirtualKey(ctx, id); err != nil {
							logger.Warn("FullReload: RemoveVirtualKey %s failed: %v", id, err)
						}
					}
				}
			}
		}

		teams, err := s.Config.ConfigStore.GetTeams(ctx, "")
		if err != nil {
			logger.Warn("FullReload: failed to list teams: %v", err)
		} else {
			dbTeamSet := make(map[string]bool)
			for _, t := range teams {
				dbTeamSet[t.ID] = true
				if _, err := s.ReloadTeam(ctx, t.ID); err != nil {
					logger.Warn("FullReload: team %s reload failed: %v", t.ID, err)
				}
			}
			if govData != nil {
				for id := range govData.Teams {
					if !dbTeamSet[id] {
						if err := s.RemoveTeam(ctx, id); err != nil {
							logger.Warn("FullReload: RemoveTeam %s failed: %v", id, err)
						}
					}
				}
			}
		}

		customers, err := s.Config.ConfigStore.GetCustomers(ctx)
		if err != nil {
			logger.Warn("FullReload: failed to list customers: %v", err)
		} else {
			dbCustSet := make(map[string]bool)
			for _, c := range customers {
				dbCustSet[c.ID] = true
				if _, err := s.ReloadCustomer(ctx, c.ID); err != nil {
					logger.Warn("FullReload: customer %s reload failed: %v", c.ID, err)
				}
			}
			if govData != nil {
				for id := range govData.Customers {
					if !dbCustSet[id] {
						if err := s.RemoveCustomer(ctx, id); err != nil {
							logger.Warn("FullReload: RemoveCustomer %s failed: %v", id, err)
						}
					}
				}
			}
		}

		routingRules, err := s.Config.ConfigStore.GetRoutingRules(ctx)
		if err != nil {
			logger.Warn("FullReload: failed to list routing rules: %v", err)
		} else {
			dbRRSet := make(map[string]bool)
			for _, r := range routingRules {
				dbRRSet[r.ID] = true
				if err := s.ReloadRoutingRule(ctx, r.ID); err != nil {
					logger.Warn("FullReload: routing rule %s reload failed: %v", r.ID, err)
				}
			}
			if govData != nil {
				for id := range govData.RoutingRules {
					if !dbRRSet[id] {
						if err := s.RemoveRoutingRule(ctx, id); err != nil {
							logger.Warn("FullReload: RemoveRoutingRule %s failed: %v", id, err)
						}
					}
				}
			}
		}
	}

	mcpConfig, err := s.Config.ConfigStore.GetMCPConfig(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to get MCP config: %v", err)
	} else if mcpConfig != nil {
		dbMCPSet := make(map[string]bool)
		for _, client := range mcpConfig.ClientConfigs {
			if client == nil {
				continue
			}
			dbMCPSet[client.ID] = true
			if err := s.ReconnectMCPClient(ctx, client.ID); err != nil {
				logger.Warn("FullReload: MCP client %s reconnect failed: %v", client.ID, err)
			}
		}
		if existingClients, err := s.Client.GetMCPClients(); err == nil {
			for _, ec := range existingClients {
				if ec.Config == nil {
					continue
				}
				if !dbMCPSet[ec.Config.ID] {
					if err := s.RemoveMCPClient(ctx, ec.Config.ID); err != nil {
						logger.Warn("FullReload: RemoveMCPClient %s failed: %v", ec.Config.ID, err)
					}
				}
			}
		}
	}

	if err := s.reconcilePlugins(ctx); err != nil {
		logger.Warn("FullReload: plugin reconciliation failed: %v", err)
	}

	return nil
}

func (s *BifrostHTTPServer) reconcilePlugins(ctx context.Context) error {
	dbPlugins, err := s.Config.ConfigStore.GetPlugins(ctx)
	if err != nil {
		return fmt.Errorf("list plugins from DB: %w", err)
	}

	memPluginsBefore := s.GetPluginStatus(ctx)
	dbEnabled := make(map[string]*tables.TablePlugin)

	for _, p := range dbPlugins {
		if !p.Enabled {
			if _, ok := memPluginsBefore[p.Name]; ok {
				if err := s.RemovePlugin(ctx, p.Name); err != nil {
					logger.Warn("reconcilePlugins: failed to disable plugin %s: %v", p.Name, err)
				}
			}
			continue
		}

		dbEnabled[p.Name] = p
		if _, ok := memPluginsBefore[p.Name]; !ok {
			if err := s.ReloadPlugin(ctx, p.Name, p.Path, p.Config, p.Placement, p.Order); err != nil {
				logger.Warn("reconcilePlugins: failed to enable plugin %s: %v", p.Name, err)
			}
		}
	}

	for name := range memPluginsBefore {
		if _, ok := dbEnabled[name]; !ok {
			isEnterprise := false
			for _, ep := range enterprisePlugins {
				if ep == name {
					isEnterprise = true
					break
				}
			}
			if isEnterprise {
				continue
			}
			if err := s.RemovePlugin(ctx, name); err != nil {
				logger.Warn("reconcilePlugins: failed to remove legacy plugin %s: %v", name, err)
			}
		}
	}

	memPluginsNow := s.GetPluginStatus(ctx)
	for name, p := range dbEnabled {
		if _, nowInMem := memPluginsNow[name]; !nowInMem {
			continue
		}
		if _, wasBefore := memPluginsBefore[name]; !wasBefore {
			// Loaded in the first loop this cycle; config already matches DB — skip redundant reload.
			continue
		}
		mem := s.getPluginConfig(name)
		if pluginConfigMatchesDB(mem, p) {
			continue
		}
		if err := s.ReloadPlugin(ctx, name, p.Path, p.Config, p.Placement, p.Order); err != nil {
			logger.Warn("reconcilePlugins: failed to reload plugin %s: %v", name, err)
		}
	}

	return nil
}

func pluginConfigMatchesDB(mem *schemas.PluginConfig, db *tables.TablePlugin) bool {
	if db == nil {
		return false
	}
	if mem == nil {
		return false
	}
	if !stringPtrEqual(mem.Path, db.Path) {
		return false
	}
	if !placementPtrEqual(mem.Placement, db.Placement) {
		return false
	}
	if !intPtrEqual(mem.Order, db.Order) {
		return false
	}
	return jsonConfigEqualNormalized(mem.Config, db.Config)
}

func stringPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func placementPtrEqual(a, b *schemas.PluginPlacement) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func jsonConfigEqualNormalized(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ja, jb)
}

func (s *BifrostHTTPServer) handleConfigSyncEvent(event configstore.ConfigSyncEvent) {
	ctx := context.Background()
	if s.clusterCtx != nil {
		ctx = s.clusterCtx
	}
	logger.Debug("cluster sync: handler apply type=%s action=%s id=%s", event.Type, event.Action, event.ID)

	switch event.Type {
	case configstore.EventTypeFullReload:
		if err := s.FullReload(ctx); err != nil {
			logger.Warn("cluster: FullReload failed: %v", err)
		}
	case configstore.EventTypeProvider:
		if event.Action == configstore.ActionDelete {
			skipCtx := context.WithValue(ctx, schemas.BifrostContextKeySkipDBUpdate, true)
			_ = s.RemoveProvider(skipCtx, schemas.ModelProvider(event.ID))
		} else {
			_, _ = s.ReloadProvider(ctx, schemas.ModelProvider(event.ID))
		}
	case configstore.EventTypeVirtualKey:
		if event.Action == configstore.ActionDelete {
			_ = s.RemoveVirtualKey(ctx, event.ID)
		} else {
			_, _ = s.ReloadVirtualKey(ctx, event.ID)
		}
	case configstore.EventTypeTeam:
		if event.Action == configstore.ActionDelete {
			_ = s.RemoveTeam(ctx, event.ID)
		} else {
			_, _ = s.ReloadTeam(ctx, event.ID)
		}
	case configstore.EventTypeCustomer:
		if event.Action == configstore.ActionDelete {
			_ = s.RemoveCustomer(ctx, event.ID)
		} else {
			_, _ = s.ReloadCustomer(ctx, event.ID)
		}
	case configstore.EventTypeModelConfig:
		if event.Action == configstore.ActionDelete {
			_ = s.RemoveModelConfig(ctx, event.ID)
		} else {
			_, _ = s.ReloadModelConfig(ctx, event.ID)
		}
	case configstore.EventTypeRoutingRule:
		if event.Action == configstore.ActionDelete {
			_ = s.RemoveRoutingRule(ctx, event.ID)
		} else {
			_ = s.ReloadRoutingRule(ctx, event.ID)
		}
	case configstore.EventTypeMCPClient:
		if event.Action == configstore.ActionDelete {
			_ = s.RemoveMCPClient(ctx, event.ID)
		} else {
			_ = s.ReconnectMCPClient(ctx, event.ID)
		}
	case configstore.EventTypePlugin:
		if event.Action == configstore.ActionDelete {
			if st, ok := s.Config.GetPluginStatusByName(event.ID); ok {
				_ = s.RemovePlugin(ctx, st.Name)
			}
		} else {
			if p, err := s.Config.ConfigStore.GetPlugin(ctx, event.ID); err == nil && p != nil {
				_ = s.ReloadPlugin(ctx, event.ID, p.Path, p.Config, p.Placement, p.Order)
			}
		}
	case configstore.EventTypeClientConfig:
		_ = s.ReloadClientConfigFromConfigStore(ctx)
	case configstore.EventTypeAuthConfig:
		// Read from DB; update AuthMiddleware in-memory only. Do NOT call s.UpdateAuthConfig —
		// that method also writes to DB, which would cause a double-write on peer nodes.
		if config, err := s.Config.ConfigStore.GetAuthConfig(ctx); err == nil && config != nil {
			if s.AuthMiddleware != nil {
				s.AuthMiddleware.UpdateAuthConfig(config)
			}
		} else if err != nil {
			logger.Warn("cluster: auth config reload failed: %v", err)
		}
	case configstore.EventTypeProxyConfig:
		// ReloadProxyConfig is in-memory only (sets s.Config.ProxyConfig).
		// (nil, nil) from GetProxyConfig means no proxy in DB — pass nil through to clear RAM.
		if config, err := s.Config.ConfigStore.GetProxyConfig(ctx); err == nil {
			_ = s.ReloadProxyConfig(ctx, config)
		} else {
			logger.Warn("cluster: proxy config reload failed: %v", err)
		}
	case configstore.EventTypeFrameworkConfig:
		// Read TableFrameworkConfig from DB, map pricing fields, then call UpdateSyncConfig.
		if dbConfig, err := s.Config.ConfigStore.GetFrameworkConfig(ctx); err == nil && dbConfig != nil {
			if s.Config.FrameworkConfig == nil {
				s.Config.FrameworkConfig = &framework.FrameworkConfig{}
			}
			s.Config.FrameworkConfig.Pricing = frameworkPricingConfig(dbConfig)
			if err := s.UpdateSyncConfig(ctx); err != nil {
				logger.Warn("cluster: framework config sync failed: %v", err)
			}
		} else if err != nil {
			logger.Warn("cluster: framework config reload failed: %v", err)
		}
	case configstore.EventTypePricingOverride:
		// UpsertPricingOverride / DeletePricingOverride are in-memory only — safe on peer nodes.
		if event.Action == configstore.ActionDelete {
			_ = s.DeletePricingOverride(ctx, event.ID)
		} else {
			if override, err := s.Config.ConfigStore.GetPricingOverrideByID(ctx, event.ID); err == nil && override != nil {
				_ = s.UpsertPricingOverride(ctx, override)
			} else if err != nil {
				logger.Warn("cluster: pricing override %s reload failed: %v", event.ID, err)
			}
		}
	default:
		logger.Warn("cluster: unknown sync event type=%q action=%q id=%s", event.Type, event.Action, event.ID)
	}
}

// frameworkPricingConfig maps a DB framework row to the in-memory modelcatalog pricing config.
// handleConfigSyncEvent and FullReload both use this so new TableFrameworkConfig fields stay consistent.
func frameworkPricingConfig(db *tables.TableFrameworkConfig) *modelcatalog.Config {
	return &modelcatalog.Config{
		PricingURL:          db.PricingURL,
		PricingSyncInterval: db.PricingSyncInterval,
	}
}

// NewLogEntryAdded broadcasts a new log entry to the websocket clients
func (s *BifrostHTTPServer) NewLogEntryAdded(_ context.Context, logEntry *logstore.Log) error {
	if s.WebSocketHandler == nil {
		return nil
	}
	s.WebSocketHandler.BroadcastEvent("log_update", logEntry)
	return nil
}
