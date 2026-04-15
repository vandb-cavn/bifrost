package configstore

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

type eventAccumulatorKey struct{}

type eventAccumulator struct {
	events []ConfigSyncEvent
}

func withEventAccumulator(ctx context.Context, acc *eventAccumulator) context.Context {
	return context.WithValue(ctx, eventAccumulatorKey{}, acc)
}

func scheduleEvent(ctx context.Context, event ConfigSyncEvent, syncer ClusterSyncer, nodeID string) {
	if syncer == nil {
		return
	}
	event.NodeID = nodeID
	if acc, ok := ctx.Value(eventAccumulatorKey{}).(*eventAccumulator); ok && acc != nil {
		acc.events = append(acc.events, event)
		return
	}
	_ = syncer.Publish(ctx, event)
}

// ctxForTxnWrite returns the context carried by the GORM transaction when present.
// ExecuteTransaction attaches the cluster event accumulator to that context; write helpers
// must use it instead of an outer context so events are buffered until commit.
func ctxForTxnWrite(ctx context.Context, tx []*gorm.DB) context.Context {
	if len(tx) == 0 || tx[0] == nil {
		return ctx
	}
	if stmt := tx[0].Statement; stmt != nil && stmt.Context != nil {
		return stmt.Context
	}
	return ctx
}

// PublishingConfigStore wraps ConfigStore and emits ConfigSyncEvents after committed writes.
type PublishingConfigStore struct {
	ConfigStore
	syncer ClusterSyncer
	nodeID string
	logger schemas.Logger
}

// NewPublishingConfigStore wraps an existing ConfigStore.
// If syncer is nil, the decorator is a transparent pass-through (single-node mode).
func NewPublishingConfigStore(inner ConfigStore, syncer ClusterSyncer, nodeID string, logger schemas.Logger) *PublishingConfigStore {
	return &PublishingConfigStore{
		ConfigStore: inner,
		syncer:      syncer,
		nodeID:      nodeID,
		logger:      logger,
	}
}

// ExecuteTransaction is the single publish choke point for transactional writes.
func (pcs *PublishingConfigStore) ExecuteTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if pcs.syncer == nil {
		return pcs.ConfigStore.ExecuteTransaction(ctx, fn)
	}
	acc := &eventAccumulator{}
	txCtx := withEventAccumulator(ctx, acc)
	err := pcs.ConfigStore.ExecuteTransaction(txCtx, func(tx *gorm.DB) error {
		tx = tx.WithContext(txCtx)
		return fn(tx)
	})
	if err != nil {
		return err
	}
	for _, ev := range acc.events {
		if pubErr := pcs.syncer.Publish(ctx, ev); pubErr != nil {
			pcs.logger.Warn("cluster sync publish failed (postgres write succeeded): %v", pubErr)
		}
	}
	return nil
}

// --- Write overrides ---

func (pcs *PublishingConfigStore) UpdateProvidersConfig(ctx context.Context, providers map[schemas.ModelProvider]ProviderConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateProvidersConfig(ctx, providers, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) AddProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.AddProvider(ctx, provider, config, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateProvider(ctx, provider, config, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteProvider(ctx context.Context, provider schemas.ModelProvider, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteProvider(ctx, provider, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "delete", ID: string(provider)}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateVirtualKey(ctx context.Context, vk *tables.TableVirtualKey, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateVirtualKey(ctx, vk, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vk.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateVirtualKey(ctx context.Context, vk *tables.TableVirtualKey, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateVirtualKey(ctx, vk, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vk.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteVirtualKey(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteVirtualKey(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateTeam(ctx, team, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "team", Action: "upsert", ID: team.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateTeam(ctx, team, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "team", Action: "upsert", ID: team.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteTeam(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteTeam(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "team", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateCustomer(ctx context.Context, c *tables.TableCustomer, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateCustomer(ctx, c, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "customer", Action: "upsert", ID: c.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateCustomer(ctx context.Context, c *tables.TableCustomer, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateCustomer(ctx, c, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "customer", Action: "upsert", ID: c.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteCustomer(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteCustomer(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "customer", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateModelConfig(ctx context.Context, mc *tables.TableModelConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateModelConfig(ctx, mc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "model_config", Action: "upsert", ID: mc.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateModelConfig(ctx context.Context, mc *tables.TableModelConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateModelConfig(ctx, mc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "model_config", Action: "upsert", ID: mc.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateModelConfigs(ctx context.Context, modelConfigs []*tables.TableModelConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateModelConfigs(ctx, modelConfigs, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteModelConfig(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteModelConfig(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "model_config", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateRoutingRule(ctx, rule, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "routing_rule", Action: "upsert", ID: rule.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateRoutingRule(ctx, rule, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "routing_rule", Action: "upsert", ID: rule.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteRoutingRule(ctx context.Context, id string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteRoutingRule(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "routing_rule", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateMCPClientConfig(ctx context.Context, cc *schemas.MCPClientConfig) error {
	if err := pcs.ConfigStore.CreateMCPClientConfig(ctx, cc); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "upsert", ID: cc.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateMCPClientConfig(ctx context.Context, id string, cc *tables.TableMCPClient) error {
	if err := pcs.ConfigStore.UpdateMCPClientConfig(ctx, id, cc); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "upsert", ID: id, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteMCPClientConfig(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteMCPClientConfig(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreatePlugin(ctx, plugin, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "upsert", ID: plugin.Name, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpsertPlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpsertPlugin(ctx, plugin, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "upsert", ID: plugin.Name, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdatePlugin(ctx, plugin, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "upsert", ID: plugin.Name, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeletePlugin(ctx context.Context, name string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeletePlugin(ctx, name, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "delete", ID: name}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateClientConfig(ctx context.Context, config *ClientConfig) error {
	if err := pcs.ConfigStore.UpdateClientConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateAuthConfig(ctx context.Context, config *AuthConfig) error {
	if err := pcs.ConfigStore.UpdateAuthConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateProxyConfig(ctx context.Context, config *tables.GlobalProxyConfig) error {
	if err := pcs.ConfigStore.UpdateProxyConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateFrameworkConfig(ctx context.Context, config *tables.TableFrameworkConfig) error {
	if err := pcs.ConfigStore.UpdateFrameworkConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateProviderKey(ctx context.Context, provider schemas.ModelProvider, key schemas.Key, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateProviderKey(ctx, provider, key, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, key schemas.Key, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateProviderKey(ctx, provider, keyID, key, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteProviderKey(ctx, provider, keyID, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateVirtualKeyProviderConfig(ctx context.Context, vkpc *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateVirtualKeyProviderConfig(ctx, vkpc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkpc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateVirtualKeyProviderConfig(ctx context.Context, vkpc *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateVirtualKeyProviderConfig(ctx, vkpc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkpc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteVirtualKeyProviderConfig(ctx context.Context, id uint, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteVirtualKeyProviderConfig(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateVirtualKeyMCPConfig(ctx context.Context, vkmc *tables.TableVirtualKeyMCPConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateVirtualKeyMCPConfig(ctx, vkmc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkmc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateVirtualKeyMCPConfig(ctx context.Context, vkmc *tables.TableVirtualKeyMCPConfig, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateVirtualKeyMCPConfig(ctx, vkmc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkmc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteVirtualKeyMCPConfig(ctx context.Context, id uint, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteVirtualKeyMCPConfig(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateMCPClientDiscoveredTools(ctx context.Context, clientID string, tools map[string]schemas.ChatTool, toolNameMapping map[string]string) error {
	if err := pcs.ConfigStore.UpdateMCPClientDiscoveredTools(ctx, clientID, tools, toolNameMapping); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "upsert", ID: clientID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeletePricingOverride(ctx context.Context, id string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeletePricingOverride(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateRateLimit(ctx context.Context, rl *tables.TableRateLimit, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateRateLimit(ctx, rl, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateRateLimit(ctx context.Context, rl *tables.TableRateLimit, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateRateLimit(ctx, rl, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateRateLimits(ctx context.Context, rls []*tables.TableRateLimit, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateRateLimits(ctx, rls, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteRateLimit(ctx context.Context, id string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteRateLimit(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateBudget(ctx context.Context, b *tables.TableBudget, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreateBudget(ctx, b, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateBudget(ctx context.Context, b *tables.TableBudget, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateBudget(ctx, b, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateBudgets(ctx context.Context, bs []*tables.TableBudget, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdateBudgets(ctx, bs, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteBudget(ctx context.Context, id string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeleteBudget(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}
