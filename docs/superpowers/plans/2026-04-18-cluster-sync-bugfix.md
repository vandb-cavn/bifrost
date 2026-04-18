# Cluster Config Sync Bug Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 5 bugs in the cluster config sync layer that cause incorrect in-memory state on peer nodes when config changes are made.

**Architecture:** All fixes are surgical edits to two files — `framework/configstore/publishing_config_store.go` and `transports/bifrost-http/server/server.go`. No new abstractions, no interface changes. Each bug is independent and can be fixed and committed separately.

**Tech Stack:** Go, Redis Streams (XADD/XREAD), GORM, fasthttp. Branch: `feat/guardrails-multi-node`.

---

## Bug Map

| # | Severity | File | Description |
|---|----------|------|-------------|
| 1 | Critical | `server/server.go:1219` | `handleConfigSyncEvent` provider delete — no `skipDBUpdate` → cố delete DB lần 2 → cleanup bị skip |
| 2 | Critical | `server/server.go:726` | `Server.RemoveProvider` returns early if governance not loaded → ModelCatalog không được cleanup |
| 3 | Important | `server/server.go:904` | `FullReload` stale provider removal gated behind `govOK` AND thiếu `skipDBUpdate` |
| 4 | Important | `server/server.go:618` | TOCTOU trong `ReloadProvider` — `AddProvider` trả `ErrAlreadyExists` không fallback sang `UpdateProviderConfig` |
| 5 | Important | `publishing_config_store.go` | `UpdateVectorStoreConfig`/`UpdateLogsStoreConfig` không publish event → peer nodes không sync |

---

## Files Modified

- **Modify:** `framework/configstore/publishing_config_store.go` — thêm 2 wrapper methods
- **Modify:** `transports/bifrost-http/server/server.go` — fix 4 bugs độc lập nhau

---

## Task 1: Fix Bug 1 — provider delete trong handleConfigSyncEvent thiếu skipDBUpdate

**Files:**
- Modify: `transports/bifrost-http/server/server.go:1217-1222`

**Vấn đề:** Khi node nhận event `provider/delete`, gọi `s.RemoveProvider(ctx, ...)` không có `skipDBUpdate=true`. `Config.RemoveProvider` cố `DELETE` DB record đã bị xóa bởi peer node → `configstore.ErrNotFound` → error không match `lib.ErrNotFound` → return sớm → `governancePlugin.DeleteProviderInMemory` và `ModelCatalog.DeleteModelDataForProvider` **không được gọi**.

- [ ] **Step 1: Locate the case block**

File: `transports/bifrost-http/server/server.go`, tìm đoạn (khoảng line 1217):
```go
case "provider":
    if event.Action == "delete" {
        _ = s.RemoveProvider(ctx, schemas.ModelProvider(event.ID))
    } else {
        _, _ = s.ReloadProvider(ctx, schemas.ModelProvider(event.ID))
    }
```

- [ ] **Step 2: Apply fix — thêm skipCtx cho delete path**

Thay đoạn trên bằng:
```go
case "provider":
    if event.Action == "delete" {
        skipCtx := context.WithValue(ctx, schemas.BifrostContextKeySkipDBUpdate, true)
        _ = s.RemoveProvider(skipCtx, schemas.ModelProvider(event.ID))
    } else {
        _, _ = s.ReloadProvider(ctx, schemas.ModelProvider(event.ID))
    }
```

- [ ] **Step 3: Build để kiểm tra không có compile error**

```bash
cd /path/to/bifost2/transports && go build ./bifrost-http/...
```
Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "fix(server): pass skipDBUpdate on peer-sync provider delete event

When a peer node deletes a provider, handleConfigSyncEvent called
RemoveProvider without skipDBUpdate=true. Config.RemoveProvider then
tried to DELETE the already-gone DB record, got configstore.ErrNotFound
which did not match lib.ErrNotFound sentinel, returned early — leaving
Config.Providers and ModelCatalog with stale data.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: Fix Bug 2 — Server.RemoveProvider fails wenn governance nicht geladen

**Files:**
- Modify: `transports/bifrost-http/server/server.go:714-737`

**Vấn đề:** `Server.RemoveProvider` gọi `s.getGovernancePlugin()` và return error ngay nếu governance không được load. Kết quả: `s.Config.ModelCatalog.DeleteModelDataForProvider(provider)` **không bao giờ được gọi** khi governance plugin không có mặt. Provider bị xóa khỏi bifrost client nhưng còn trong ModelCatalog.

- [ ] **Step 1: Locate RemoveProvider**

File: `transports/bifrost-http/server/server.go`, tìm hàm `RemoveProvider` (khoảng line 714):
```go
func (s *BifrostHTTPServer) RemoveProvider(ctx context.Context, provider schemas.ModelProvider) error {
	err := s.Client.RemoveProvider(provider)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		logger.Error("failed to remove provider from client: %v", err)
		return err
	}
	err = s.Config.RemoveProvider(ctx, provider)
	if err != nil && !errors.Is(err, lib.ErrNotFound) {
		logger.Error("failed to remove provider from config: %v. Client and config may be out of sync, please restart bifrost", err)
		return fmt.Errorf("failed to remove provider from config: %w. Client and config may be out of sync, please restart bifrost", err)
	}
	governancePlugin, err := s.getGovernancePlugin()
	if err != nil {
		return err
	}
	governancePlugin.GetGovernanceStore().DeleteProviderInMemory(string(provider))
	if s.Config == nil || s.Config.ModelCatalog == nil {
		return fmt.Errorf("pricing manager not found")
	}
	s.Config.ModelCatalog.DeleteModelDataForProvider(provider)

	return nil
}
```

- [ ] **Step 2: Apply fix — governance cleanup optional, ModelCatalog always cleaned**

Thay toàn bộ hàm `RemoveProvider`:
```go
func (s *BifrostHTTPServer) RemoveProvider(ctx context.Context, provider schemas.ModelProvider) error {
	err := s.Client.RemoveProvider(provider)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		logger.Error("failed to remove provider from client: %v", err)
		return err
	}
	err = s.Config.RemoveProvider(ctx, provider)
	if err != nil && !errors.Is(err, lib.ErrNotFound) {
		logger.Error("failed to remove provider from config: %v. Client and config may be out of sync, please restart bifrost", err)
		return fmt.Errorf("failed to remove provider from config: %w. Client and config may be out of sync, please restart bifrost", err)
	}
	// Governance cleanup is optional — not all deployments load the governance plugin.
	if governancePlugin, err := s.getGovernancePlugin(); err == nil {
		governancePlugin.GetGovernanceStore().DeleteProviderInMemory(string(provider))
	}
	if s.Config != nil && s.Config.ModelCatalog != nil {
		s.Config.ModelCatalog.DeleteModelDataForProvider(provider)
	}
	return nil
}
```

- [ ] **Step 3: Build**

```bash
cd /path/to/bifost2/transports && go build ./bifrost-http/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "fix(server): always clean ModelCatalog on RemoveProvider regardless of governance

RemoveProvider returned early when the governance plugin was not loaded,
skipping ModelCatalog.DeleteModelDataForProvider. Governance cleanup is
now best-effort (warn-only), ensuring ModelCatalog is always cleaned up.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: Fix Bug 3 — FullReload stale provider gated + thiếu skipDBUpdate

**Files:**
- Modify: `transports/bifrost-http/server/server.go:904-912`

**Vấn đề A:** Stale provider cleanup chỉ chạy khi `govOK == true`. Nếu governance plugin không load, providers bị xóa trên peer node sẽ tồn tại mãi trong `Config.Providers` cho đến khi restart.

**Vấn đề B:** `RemoveProvider` được gọi mà không có `skipDBUpdate=true`. Provider này đã bị xóa khỏi DB (vì `FullReload` nhận danh sách hiện tại từ DB), nên thao tác DELETE sẽ fail với `ErrNotFound`.

- [ ] **Step 1: Locate the stale cleanup block**

File: `transports/bifrost-http/server/server.go`, tìm đoạn (khoảng line 904):
```go
		if govOK {
			for _, mp := range inMemProviders {
				if !dbProviderSet[mp] {
					if err := s.RemoveProvider(ctx, mp); err != nil {
						logger.Warn("FullReload: RemoveProvider %s failed: %v", mp, err)
					}
				}
			}
		}
```

- [ ] **Step 2: Apply fix — ungated + skipDBUpdate**

Thay đoạn trên bằng:
```go
		// Remove stale providers regardless of governance state — DB is authoritative.
		// Pass skipDBUpdate=true: these providers are already absent from DB (confirmed
		// by the GetProviders query above), so a second DELETE would fail with ErrNotFound.
		skipCtx := context.WithValue(ctx, schemas.BifrostContextKeySkipDBUpdate, true)
		for _, mp := range inMemProviders {
			if !dbProviderSet[mp] {
				if err := s.RemoveProvider(skipCtx, mp); err != nil {
					logger.Warn("FullReload: RemoveProvider %s failed: %v", mp, err)
				}
			}
		}
```

- [ ] **Step 3: Build**

```bash
cd /path/to/bifost2/transports && go build ./bifrost-http/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "fix(server): ungate stale provider cleanup from govOK in FullReload

Stale provider removal was gated behind govOK, so deployments without
the governance plugin accumulated deleted providers in Config.Providers.
Also adds skipDBUpdate=true since the providers are already absent from
DB — the prior DELETE would fail with ErrNotFound.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: Fix Bug 4 — TOCTOU trong ReloadProvider, không fallback khi AddProvider trả ErrAlreadyExists

**Files:**
- Modify: `transports/bifrost-http/server/server.go:611-635`

**Vấn đề:** Giữa `GetProviderKeysRaw` (thả RLock) và `AddProvider` (cần Lock), một goroutine khác có thể thêm provider. Khi đó `AddProvider` trả `ErrAlreadyExists` nhưng code chỉ log warning và không update provider keys — provider ở trong memory nhưng thiếu keys mới.

- [ ] **Step 1: Locate the sync block trong ReloadProvider**

File: `transports/bifrost-http/server/server.go`, tìm đoạn (khoảng line 611):
```go
	if dbProviderConfig, err := s.Config.ConfigStore.GetProviderConfig(ctx, provider); err == nil && dbProviderConfig != nil {
		skipCtx := context.WithValue(ctx, schemas.BifrostContextKeySkipDBUpdate, true)
		_, inMemErr := s.Config.GetProviderKeysRaw(provider)
		if errors.Is(inMemErr, lib.ErrNotFound) {
			// Provider is new on this node — add it
			if addErr := s.Config.AddProvider(skipCtx, provider, *dbProviderConfig); addErr != nil {
				logger.Warn("ReloadProvider: failed to add new provider %s to memory: %v", provider, addErr)
			}
		} else {
			// Provider already in memory — update keys/config
			if updateErr := s.Config.UpdateProviderConfig(skipCtx, provider, *dbProviderConfig); updateErr != nil {
				logger.Warn("ReloadProvider: failed to sync provider config to memory for %s: %v", provider, updateErr)
			}
		}
	} else if err != nil {
		logger.Warn("ReloadProvider: failed to fetch provider config from DB for %s: %v", provider, err)
	}
```

- [ ] **Step 2: Apply fix — fallback sang UpdateProviderConfig khi AddProvider trả ErrAlreadyExists**

Thay toàn bộ đoạn trên:
```go
	if dbProviderConfig, err := s.Config.ConfigStore.GetProviderConfig(ctx, provider); err == nil && dbProviderConfig != nil {
		skipCtx := context.WithValue(ctx, schemas.BifrostContextKeySkipDBUpdate, true)
		_, inMemErr := s.Config.GetProviderKeysRaw(provider)
		if errors.Is(inMemErr, lib.ErrNotFound) {
			// Provider not yet in memory — add it. If a concurrent goroutine added it
			// between GetProviderKeysRaw and AddProvider (TOCTOU), fall back to update.
			if addErr := s.Config.AddProvider(skipCtx, provider, *dbProviderConfig); addErr != nil {
				if errors.Is(addErr, lib.ErrAlreadyExists) {
					if updateErr := s.Config.UpdateProviderConfig(skipCtx, provider, *dbProviderConfig); updateErr != nil {
						logger.Warn("ReloadProvider: failed to sync provider config to memory for %s: %v", provider, updateErr)
					}
				} else {
					logger.Warn("ReloadProvider: failed to add new provider %s to memory: %v", provider, addErr)
				}
			}
		} else {
			// Provider already in memory — update keys/config
			if updateErr := s.Config.UpdateProviderConfig(skipCtx, provider, *dbProviderConfig); updateErr != nil {
				logger.Warn("ReloadProvider: failed to sync provider config to memory for %s: %v", provider, updateErr)
			}
		}
	} else if err != nil {
		logger.Warn("ReloadProvider: failed to fetch provider config from DB for %s: %v", provider, err)
	}
```

- [ ] **Step 3: Build**

```bash
cd /path/to/bifost2/transports && go build ./bifrost-http/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "fix(server): fallback to UpdateProviderConfig on ErrAlreadyExists in ReloadProvider

TOCTOU: between GetProviderKeysRaw (releases RLock) and AddProvider
(acquires Lock), a concurrent goroutine could add the provider. In that
case AddProvider returns ErrAlreadyExists — previously only logged as
warning, leaving keys un-synced. Now falls back to UpdateProviderConfig.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: Fix Bug 5 — UpdateVectorStoreConfig và UpdateLogsStoreConfig không publish event

**Files:**
- Modify: `framework/configstore/publishing_config_store.go`

**Vấn đề:** `UpdateVectorStoreConfig` và `UpdateLogsStoreConfig` không được override trong `PublishingConfigStore`. Khi một node thay đổi vector store hoặc logs store config, peer nodes không nhận được bất kỳ event nào và tiếp tục dùng config cũ.

- [ ] **Step 1: Xác nhận interface có 2 methods này**

```bash
grep -n "UpdateVectorStoreConfig\|UpdateLogsStoreConfig" /path/to/bifost2/framework/configstore/store.go
```
Expected:
```
127:	UpdateVectorStoreConfig(ctx context.Context, config *vectorstore.Config) error
131:	UpdateLogsStoreConfig(ctx context.Context, config *logstore.Config) error
```

- [ ] **Step 2: Tìm vị trí thêm vào publishing_config_store.go**

Tìm cuối file `framework/configstore/publishing_config_store.go`. Thêm sau hàm `DeleteBudget` (hoặc cuối file trước closing brace).

- [ ] **Step 3: Thêm 2 wrapper methods**

Append vào cuối `framework/configstore/publishing_config_store.go`:
```go
func (pcs *PublishingConfigStore) UpdateVectorStoreConfig(ctx context.Context, config *vectorstore.Config) error {
	if err := pcs.ConfigStore.UpdateVectorStoreConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateLogsStoreConfig(ctx context.Context, config *logstore.Config) error {
	if err := pcs.ConfigStore.UpdateLogsStoreConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}
```

Lưu ý: Kiểm tra import packages đầu file. Nếu `vectorstore` và `logstore` chưa được import, thêm vào:
```go
import (
    // existing imports...
    "github.com/maximhq/bifrost/framework/logstore"
    "github.com/maximhq/bifrost/framework/vectorstore"
)
```

- [ ] **Step 4: Kiểm tra import packages thực tế**

```bash
grep -n "vectorstore\|logstore" /path/to/bifost2/framework/configstore/publishing_config_store.go | head -5
```

Nếu chưa có, thêm vào import block.

- [ ] **Step 5: Build**

```bash
cd /path/to/bifost2/framework && go build ./...
```
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add framework/configstore/publishing_config_store.go
git commit -m "fix(configstore): publish full_reload on VectorStore and LogsStore config changes

UpdateVectorStoreConfig and UpdateLogsStoreConfig were not overridden in
PublishingConfigStore, so peer nodes never received sync events when these
configs changed at runtime.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 6: Cherry-pick tất cả fixes sang feat/guardrails-plugin (nếu cần)

- [ ] **Step 1: Lấy SHA của 5 commits vừa tạo**

```bash
git log --oneline -5
```

- [ ] **Step 2: Switch sang feat/guardrails-plugin và cherry-pick**

```bash
git checkout feat/guardrails-plugin
git cherry-pick <sha1> <sha2> <sha3> <sha4> <sha5>
```

- [ ] **Step 3: Build lại để confirm**

```bash
cd /path/to/bifost2/transports && go build ./bifrost-http/...
cd /path/to/bifost2/framework && go build ./...
```

- [ ] **Step 4: Switch về feat/guardrails-multi-node**

```bash
git checkout feat/guardrails-multi-node
```

---

## Verification

Sau khi fix xong tất cả tasks, kiểm tra thủ công:

1. **Provider delete sync:** Thêm provider trên node 1, xóa trên node 2 → node 1 không còn thấy provider trong `/api/providers`
2. **Provider key sync:** Thêm provider + key trên node 2 → node 1 có thể dùng provider đó để gọi API
3. **Stale cleanup:** Restart node 2 sau khi xóa provider → node 2 không có provider đó trong memory
4. **Build:** `go build ./...` trong cả `framework/` và `transports/` — không có lỗi
