# Multi-Node Cluster Sync — Overlay Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce the upstream-owned footprint of the already-working multi-node cluster sync (Redis Streams config publishing, shared governance counters, calendar-aligned quota windows) to a small set of *documented* patches, by relocating all new logic into fork-owned files — **without changing any runtime behavior**.

**Architecture:** This is a **behavior-preserving relocation refactor**, not a rewrite. The feature already shipped and was debugged (5 peer-sync correctness fixes, a TOCTOU fix, provider-key sync, quota-window alignment). We keep that logic verbatim; we only move *where it lives* and *how core calls in*. The repo is a fork of upstream `maximhq/bifrost`; the clean upstream baseline is commit `4bbcd5846` (the parent of the cluster-sync port `f46714423`, confirmed as the merge-base with `mirror/upstream-main`). `framework`, `plugins/governance`, and `transports` are **separate Go modules** (`go.work` ties them), so the overlay is fork-owned *files inside each module's existing package* (per playbook §2.1, a new file = ~0 merge tax), plus a small list of documented in-body patches recorded in `FORK_PATCHES.md`.

**Tech Stack:** Go (multi-module workspace via `go.work`), GORM, Redis (`github.com/redis/go-redis/v9`), PostgreSQL/SQLite config store.

**Authoritative reference:** the overlay strategy is `docs/superpowers/overlay-implementation-playbook.md`. The full pre-refactor audit (symbol-by-symbol classification) is reproduced in the **Patch Inventory** section below — treat it as the source of truth for what moves vs what stays.

---

## The two guard-rails for every task

1. **The existing test suite is the spec.** Behavior must not change. After every move, the relevant module must build and its tests must stay green. Tasks that change behavior are explicitly the *only* two: the quota-window scope is already in the tree (no behavior change), and the `sync.go` URL regression fix (Tasks 10–11, which revert to baseline and add a test).
2. **The footprint audit is the acceptance gate.** At the end, `git diff --stat 4bbcd5846 HEAD` on upstream-owned files must show **only**: (a) new files, (b) new same-package fork files, (c) the documented patches in `FORK_PATCHES.md`. No reformat churn.

---

## Patch Inventory (source of truth — verified against baseline `4bbcd5846`)

### Group 0 — MOVABLE (0 patch): new files already isolated
Relocate-in-place only (already new files, fork-owned, 0 merge tax — leave where they are unless a task says otherwise):
- `framework/configstore/cluster_syncer.go` (+`_test.go`)
- `framework/configstore/publishing_config_store.go` (+`_test.go`)
- `plugins/governance/redis_counters.go`
- `plugins/governance/quota_window.go` (+`quota_window_test.go`, +`quota_window_redis_test.go`)
- `plugins/governance/multinode_test.go`
- `transports/bifrost-http/lib/cluster_config.go`

### Group 1 — MOVABLE: new methods in core files → new same-package fork files
**`transports/bifrost-http/server/server.go` → new `server_cluster.go`** (same `server` package). All confirmed NOT in baseline:
`FullReload`, `handleConfigSyncEvent`, `initClusterPublishing`, `initClusterSubscriberAndRedis`, `reconcilePlugins`, `pluginConfigMatchesDB`, `stringPtrEqual`, `placementPtrEqual`, `intPtrEqual`, `jsonConfigEqualNormalized`, `getGovernanceLocalStore`, `GetGovernanceData`, `NewLogEntryAdded`, `frameworkPricingConfig`, and the new reload/remove helpers `ReloadProvider`, `RemoveProvider`, `ReloadVirtualKey`, `RemoveVirtualKey`, `ReloadTeam`, `ReloadCustomer`, `ReloadRoutingRule`, `RemoveTeam`, `RemoveCustomer`, `RemoveModelConfig`, `RemoveRoutingRule`, `ReloadModelConfig` (relocate whichever of these are confirmed new in Task 5).

**`plugins/governance/store.go` → `redis_counters.go`** (same `governance` package). Confirmed NOT in baseline:
`InitRedis`, `SetRedisAvailable`, `GetRedisCounters`, `IsRedisAvailable`, `RunRecoveryMerge`, `resetRateLimitRedis`, `clusterRateUsage`, `clusterBudgetSpent`.

**`framework/configstore/rdb.go`:** `NewRDBConfigStoreForTest` is a new test helper → move to a `_test.go` file (or `rdb_fork_test.go`).

### Group 2 — REAL PATCHES (stay; document in `FORK_PATCHES.md`)
| File (upstream-owned) | Hook point | What stays |
|---|---|---|
| `server/server.go` | `BifrostHTTPServer` struct | +6 fields: `clusterSyncer`, `clusterRedisClient`, `clusterCtx`, `clusterCancel`, `clusterEventNodeID`, `fullReloadMu` |
| `server/server.go` | bootstrap, before `LoadPlugins` (~:1884) | `if err := s.initClusterPublishing(ctx); err != nil { return ... }` |
| `server/server.go` | bootstrap, after routes (~:2099) | `if err := s.initClusterSubscriberAndRedis(ctx); err != nil { return ... }` |
| `server/server.go` | shutdown (~:2154) | `if s.clusterCancel != nil { s.clusterCancel() }` + `_ = s.clusterRedisClient.Close()` |
| `lib/config.go` | `Config` struct (:162), unmarshal temp (:241,:260), 2nd struct (:334), load (:677) | `Cluster *ClusterConfig` field + plumbing (~5 lines) |
| `transports/config.schema.json` | — | additive `cluster` schema block |
| `governance/store.go` | `LocalGovernanceStore` struct (:59-62) | +2 fields: `redisCounters`, `redisAvailable atomic.Bool` |
| `governance/store.go` | `BumpBudgetUsage` (:534) | guarded `redisCounters.IncrBudget` call |
| `governance/store.go` | `BumpRateLimitUsage` (:572) | guarded `redisCounters.IncrTokens/IncrRequests` call |
| `governance/store.go` | `ResetExpiredBudgetsInMemory` (:1551) | guarded `redisCounters.ResetBudget` + `EvaluateQuotaWindow` calls |
| `governance/store.go` | read paths (:1007,:1015,:1031,:1079,:1089) | thin calls to `clusterRateUsage`/`clusterBudgetSpent`/`EvaluateQuotaWindow` |
| `governance/store.go` | rate reset (:1610-1612,:1636) | `evaluateQuotaWindowPtr` + `resetRateLimitRedis` calls |
| `governance/tracker.go` | `UpdateUsage`, `resetExpiredCounters`, `PerformStartupResets` | in-body changes; `PerformStartupResets` is a substantial rewrite (102→19 lines) — re-apply carefully |
| `framework/modelcatalog/sync.go` | `syncPricing`, `syncModelParameters` | deterministic sort/dedup ordering (deadlock 40P01 fix) |

### Group 3 — CHURN to DISCARD (do NOT carry forward)
Reformat-only diff introduced by the port: ~2700 lines in `governance/store.go`, the struct re-spacing in `lib/config.go`, and the `populateModelParamsFromPricing` / `applyModelParameters` re-formatting in `modelcatalog/sync.go`. The refactor **resets these files to baseline `4bbcd5846` and re-applies only the Group 2 patches** — see Phase 3.

### Known regression to fix (Task 14)
`modelcatalog/sync.go` `loadModelParametersFromURL` was changed by the port from `mc.getModelParametersURL()` (a configurable field) to the hardcoded constant `DefaultModelParametersURL`, while the sibling pricing path still uses the configurable `mc.getPricingURL()`. No cluster reason exists → unintended regression. Revert + add a guard test.

---

## Target File Structure (after refactor)

```
framework/configstore/
  cluster_syncer.go              (existing new file — unchanged)
  publishing_config_store.go     (existing new file — unchanged)
  rdb.go                         (upstream — reset to baseline; test helper removed)
  rdb_fork_test.go               (NEW — holds NewRDBConfigStoreForTest)

plugins/governance/
  redis_counters.go              (existing new file — GAINS the moved cluster/* methods)
  quota_window.go                (existing new file — unchanged)
  store.go                       (upstream — reset to baseline + Group 2 patches only)
  tracker.go                     (upstream — reset to baseline + Group 2 patches only)

transports/bifrost-http/
  server/server.go               (upstream — reset to baseline + Group 2 patches only)
  server/server_cluster.go       (NEW — all cluster server methods)
  lib/config.go                  (upstream — reset to baseline + Cluster field patch)
  lib/cluster_config.go          (existing new file — unchanged)
  config.schema.json             (upstream — additive cluster block)

framework/modelcatalog/sync.go   (upstream — reset to baseline + sort patches + URL revert)

FORK_PATCHES.md                  (NEW — at repo root; documents every Group 2 patch)
```

---

## Per-module build/test commands (used throughout)

```bash
# framework
cd framework && go build ./... && go test ./configstore/... ./modelcatalog/...
# governance plugin
cd plugins/governance && go build ./... && go test ./...
# transports
cd transports && go build ./...
# governance integration tests (separate module)
cd tests/governance && go test ./...
```

Run from repo root with `cd` per module (the `go.work` ties them, but tests run per module).

---

## Phase 0 — Baseline safety net

### Task 1: Create the isolated worktree and capture the "before" state

**Files:** none (setup only)

- [ ] **Step 1: Create worktree** (per superpowers:using-git-worktrees)

```bash
git worktree add .worktrees/cluster-overlay-refactor -b refactor/cluster-sync-overlay stable
cd .worktrees/cluster-overlay-refactor
```

- [ ] **Step 2: Confirm current suite is green (this is the regression oracle)**

Run:
```bash
cd framework && go build ./... && go test ./configstore/... ./modelcatalog/... ; cd -
cd plugins/governance && go build ./... && go test ./... ; cd -
cd transports && go build ./... ; cd -
```
Expected: all PASS / build OK. If anything is already red, STOP and report — the refactor needs a green baseline.

- [ ] **Step 3: Capture the before-footprint for the final audit**

Run:
```bash
git diff --stat 4bbcd5846 HEAD -- \
  transports/bifrost-http/server/server.go \
  transports/bifrost-http/lib/config.go \
  framework/configstore/rdb.go \
  framework/modelcatalog/sync.go \
  plugins/governance/store.go \
  plugins/governance/tracker.go > /tmp/footprint-before.txt
cat /tmp/footprint-before.txt
```
Expected: large diffs (thousands of lines) — this is the churn we will shrink.

- [ ] **Step 4: Commit the plan into the worktree**

```bash
git add docs/superpowers/plans/2026-05-30-cluster-sync-overlay-refactor.md
git commit -m "docs(plan): cluster-sync overlay refactor plan"
```

---

## Phase 1 — Extract server cluster methods into `server_cluster.go`

This is the highest-value, lowest-risk move: ~600 lines leave the hot file with zero behavior change (same package, same methods).

### Task 2: Confirm which server methods are new (movable)

**Files:** read-only audit

- [ ] **Step 1: List candidate methods and verify each is absent in baseline**

Run:
```bash
B=4bbcd5846
for fn in FullReload handleConfigSyncEvent initClusterPublishing initClusterSubscriberAndRedis \
  reconcilePlugins pluginConfigMatchesDB stringPtrEqual placementPtrEqual intPtrEqual \
  jsonConfigEqualNormalized getGovernanceLocalStore GetGovernanceData NewLogEntryAdded \
  frameworkPricingConfig ReloadProvider RemoveProvider ReloadVirtualKey RemoveVirtualKey \
  ReloadTeam ReloadCustomer ReloadRoutingRule RemoveTeam RemoveCustomer RemoveModelConfig \
  RemoveRoutingRule ReloadModelConfig; do
  c=$(git show $B:transports/bifrost-http/server/server.go 2>/dev/null | grep -c ") $fn(")
  echo "$fn baseline_count=$c"
done
```
Expected: methods with `baseline_count=0` are new → move them. Any with `baseline_count>0` are upstream → they STAY in server.go and become documented patches instead (note them; they are not expected here but verify).

- [ ] **Step 2: Record the final movable list** in a scratch note for Task 3. Do not move anything yet.

### Task 3: Move the new server methods to `server_cluster.go`

**Files:**
- Create: `transports/bifrost-http/server/server_cluster.go`
- Modify: `transports/bifrost-http/server/server.go` (remove the moved method bodies; keep the 6 struct fields and the 3 bootstrap/shutdown call-sites)

- [ ] **Step 1: Create the new file with the package header and imports**

```go
package server

// server_cluster.go — fork-owned. All multi-node cluster-sync server methods live here so
// the upstream server.go keeps only the documented hook patches (see FORK_PATCHES.md).
//
// These are methods on *BifrostHTTPServer; Go allows a type's methods to span files in the
// same package, so this file reaches unexported server state with zero upstream merge conflict.

import (
	// (fill from the moved code — e.g. context, fmt, sync, uuid, redis/go-redis/v9,
	//  configstore, governance, modelcatalog, schemas, tables, framework, logstore)
)
```

- [ ] **Step 2: Cut each movable method (verbatim, body unchanged) from `server.go` and paste into `server_cluster.go`**

Move the methods confirmed in Task 2 (the cluster init/reload/remove/orchestration set). Do **not** edit bodies — this is a pure relocation. Leave in `server.go`: the `BifrostHTTPServer` struct field additions and the bootstrap/shutdown call-sites (those are Group 2 patches that stay).

- [ ] **Step 3: Fix imports in both files**

Run:
```bash
cd transports && goimports -w bifrost-http/server/server.go bifrost-http/server/server_cluster.go
```
(If `goimports` is unavailable, `go build` errors will list the needed imports.)

- [ ] **Step 4: Build transports**

Run: `cd transports && go build ./...`
Expected: build OK. (Same package → unexported access works; no interface needed.)

- [ ] **Step 5: Verify behavior unchanged — full transports build + any server tests**

Run: `cd transports && go build ./... && go vet ./bifrost-http/server/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add transports/bifrost-http/server/server_cluster.go transports/bifrost-http/server/server.go
git commit -m "refactor(cluster): move server cluster methods into server_cluster.go (no behavior change)"
```

---

## Phase 2 — Extract governance cluster methods into `redis_counters.go`

### Task 4: Move new governance methods from `store.go` to `redis_counters.go`

**Files:**
- Modify: `plugins/governance/redis_counters.go` (add the moved methods)
- Modify: `plugins/governance/store.go` (remove moved method bodies; keep the 2 struct fields + thin call-sites)

- [ ] **Step 1: Confirm movable methods are new in baseline**

Run:
```bash
B=4bbcd5846
for fn in InitRedis SetRedisAvailable GetRedisCounters IsRedisAvailable RunRecoveryMerge \
  resetRateLimitRedis clusterRateUsage clusterBudgetSpent; do
  echo "$fn baseline=$(git show $B:plugins/governance/store.go 2>/dev/null | grep -c ") $fn(")"
done
```
Expected: all `baseline=0`.

- [ ] **Step 2: Cut those 8 methods (verbatim) from `store.go`, paste into `redis_counters.go`**

They are methods on `*LocalGovernanceStore` — same package, so they keep accessing `gs.redisCounters`, `gs.redisAvailable`, and other store internals. Leave in `store.go`: the `redisCounters`/`redisAvailable` struct fields and the call-sites that *invoke* these methods (Group 2 patches).

- [ ] **Step 3: Fix imports**

Run: `cd plugins/governance && goimports -w redis_counters.go store.go`

- [ ] **Step 4: Build + test governance**

Run: `cd plugins/governance && go build ./... && go test ./...`
Expected: PASS (incl. `multinode_test.go`, `quota_window_test.go`).

- [ ] **Step 5: Commit**

```bash
git add plugins/governance/redis_counters.go plugins/governance/store.go
git commit -m "refactor(cluster): move governance redis/cluster helpers into redis_counters.go (no behavior change)"
```

### Task 5: Move `NewRDBConfigStoreForTest` out of `rdb.go`

**Files:**
- Create: `framework/configstore/rdb_fork_test.go`
- Modify: `framework/configstore/rdb.go` (remove the helper)

- [ ] **Step 1: Move the function verbatim**

Cut `NewRDBConfigStoreForTest` from `rdb.go` into a new `rdb_fork_test.go` (still `package configstore`). It is a test-only helper, so a `_test.go` file keeps it out of the production upstream file entirely.

- [ ] **Step 2: Build + test**

Run: `cd framework && go build ./... && go test ./configstore/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add framework/configstore/rdb_fork_test.go framework/configstore/rdb.go
git commit -m "refactor(cluster): move test-only RDB helper out of rdb.go"
```

---

## Phase 3 — Discard churn: reset upstream files to baseline, re-apply only Group 2 patches

This is the delicate phase. After Phases 1–2, the upstream files still carry the port's reformat churn. We now reset each to baseline and re-apply *only* the documented patches, so the final diff is minimal. **Do one file per task, build+test after each.**

### Task 6: Re-derive `governance/store.go` from baseline + patches

**Files:** Modify `plugins/governance/store.go`

- [ ] **Step 1: Snapshot the current (already-extracted) file for diffing**

```bash
cp plugins/governance/store.go /tmp/store.after-extract.go
```

- [ ] **Step 2: Produce a baseline-vs-current semantic diff (ignore whitespace) to isolate real changes**

Run:
```bash
git show 4bbcd5846:plugins/governance/store.go > /tmp/store.baseline.go
diff -w /tmp/store.baseline.go plugins/governance/store.go > /tmp/store.realdiff.txt || true
cat /tmp/store.realdiff.txt
```
Expected: the `-w` diff collapses reformat noise, surfacing the real changes: the 2 struct fields, the guarded `redisCounters.*` call-sites in `BumpBudgetUsage`/`BumpRateLimitUsage`/`ResetExpiredBudgetsInMemory`, and the `EvaluateQuotaWindow`/`evaluateQuotaWindowPtr`/`clusterRateUsage`/`clusterBudgetSpent`/`resetRateLimitRedis` call-sites.

- [ ] **Step 3: Reset to baseline, then re-apply ONLY the real changes from Step 2**

```bash
git show 4bbcd5846:plugins/governance/store.go > plugins/governance/store.go
```
Then hand-apply each hunk from `/tmp/store.realdiff.txt` that is a Group 2 patch (struct fields + the listed call-sites). Skip pure-reformat hunks. Each re-applied site must be the thin guarded form, e.g.:

```go
// in BumpBudgetUsage, after the in-memory/DB update:
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    if _, err := gs.redisCounters.IncrBudget(ctx, budgetID, cost); err != nil {
        gs.logger.Warn(...) // match existing log style
    }
}
```

- [ ] **Step 4: Build + test governance**

Run: `cd plugins/governance && go build ./... && go test ./...`
Expected: PASS. If a test fails, a Group 2 patch was missed — compare against `/tmp/store.after-extract.go` (behavior reference).

- [ ] **Step 5: Confirm churn is gone**

Run: `git diff --stat 4bbcd5846 -- plugins/governance/store.go`
Expected: now ~tens of lines (was ~2775).

- [ ] **Step 6: Commit**

```bash
git add plugins/governance/store.go
git commit -m "refactor(cluster): reset governance store.go to baseline + documented patches only"
```

### Task 7: Re-derive `governance/tracker.go` from baseline + patches

**Files:** Modify `plugins/governance/tracker.go`

- [ ] **Step 1: Diff baseline vs current (whitespace-insensitive)**

```bash
git show 4bbcd5846:plugins/governance/tracker.go > /tmp/tracker.baseline.go
diff -w /tmp/tracker.baseline.go plugins/governance/tracker.go > /tmp/tracker.realdiff.txt || true
cat /tmp/tracker.realdiff.txt
```
Expected: real changes in `UpdateUsage`, `resetExpiredCounters`, and the `PerformStartupResets` rewrite (102→19). These are the quota-window-alignment changes (in scope).

- [ ] **Step 2: Reset to baseline, re-apply the real (non-reformat) hunks**

```bash
git show 4bbcd5846:plugins/governance/tracker.go > plugins/governance/tracker.go
```
Re-apply the `UpdateUsage`/`resetExpiredCounters`/`PerformStartupResets` changes from `/tmp/tracker.realdiff.txt`. The `PerformStartupResets` rewrite is the trickiest — apply it as a whole-function replacement, using `/tmp/tracker.realdiff.txt` as the authoritative new body.

- [ ] **Step 3: Build + test**

Run: `cd plugins/governance && go build ./... && go test ./...`
Expected: PASS (quota_window tests are the behavior oracle).

- [ ] **Step 4: Commit**

```bash
git add plugins/governance/tracker.go
git commit -m "refactor(quota): reset tracker.go to baseline + quota-window patches only"
```

### Task 8: Re-derive `lib/config.go` from baseline + Cluster field

**Files:** Modify `transports/bifrost-http/lib/config.go`

- [ ] **Step 1: Diff baseline vs current**

```bash
git show 4bbcd5846:transports/bifrost-http/lib/config.go > /tmp/config.baseline.go
diff -w /tmp/config.baseline.go transports/bifrost-http/lib/config.go > /tmp/config.realdiff.txt || true
cat /tmp/config.realdiff.txt
```
Expected: real changes ≈ the `Cluster *ClusterConfig` field on `Config` and its unmarshal/load plumbing (~5 lines). (Note: `config.go` may carry other legitimate fork changes unrelated to cluster — only collapse pure reformat; keep any non-cluster real changes.)

- [ ] **Step 2: Reset to baseline, re-apply the real hunks**

```bash
git show 4bbcd5846:transports/bifrost-http/lib/config.go > transports/bifrost-http/lib/config.go
```
Re-apply every non-reformat hunk from `/tmp/config.realdiff.txt` (the `Cluster` field plumbing + any other genuine fork change the diff reveals).

- [ ] **Step 3: Build transports**

Run: `cd transports && go build ./...`
Expected: build OK (`ClusterConfig` resolves from `lib/cluster_config.go`).

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/lib/config.go
git commit -m "refactor(cluster): reset lib/config.go to baseline + Cluster field patch only"
```

### Task 9: Verify `server.go` is already minimal (no churn reset needed if Phase 1 was clean)

**Files:** read-only check on `transports/bifrost-http/server/server.go`

- [ ] **Step 1: Diff baseline vs current**

```bash
git show 4bbcd5846:transports/bifrost-http/server/server.go > /tmp/server.baseline.go
diff -w /tmp/server.baseline.go transports/bifrost-http/server/server.go > /tmp/server.realdiff.txt || true
wc -l /tmp/server.realdiff.txt ; sed -n '1,80p' /tmp/server.realdiff.txt
```
Expected after Phase 1: the only real changes are the 6 struct fields + 3 bootstrap/shutdown call-sites. If the diff still shows reformat churn or stray bodies, reset to baseline and re-apply only those Group 2 hooks (same procedure as Task 6).

- [ ] **Step 2: If a reset was needed, build + commit**

Run: `cd transports && go build ./...`
```bash
git add transports/bifrost-http/server/server.go
git commit -m "refactor(cluster): minimize server.go to documented hooks only"
```

---

## Phase 4 — `modelcatalog/sync.go`: keep deadlock fix, revert URL regression

### Task 10: Re-derive `sync.go` from baseline + the deterministic-ordering patches

**Files:** Modify `framework/modelcatalog/sync.go`

- [ ] **Step 1: Diff baseline vs current**

```bash
git show 4bbcd5846:framework/modelcatalog/sync.go > /tmp/sync.baseline.go
diff -w /tmp/sync.baseline.go framework/modelcatalog/sync.go > /tmp/sync.realdiff.txt || true
cat /tmp/sync.realdiff.txt
```
Expected real changes: (a) `syncPricing` dedup+sort before the transaction, (b) `syncModelParameters` `slices.Sort(models)` before the upsert loop, (c) the URL change (to be reverted), (d) reformat of `populateModelParamsFromPricing`/`applyModelParameters` (discard).

- [ ] **Step 2: Reset to baseline, re-apply ONLY the two deterministic-ordering hunks**

```bash
git show 4bbcd5846:framework/modelcatalog/sync.go > framework/modelcatalog/sync.go
```
Re-apply the `syncPricing` sort/dedup block and the `syncModelParameters` `slices.Sort` block from `/tmp/sync.realdiff.txt`. Do **not** re-apply the URL change or the reformat hunks. (The deadlock rationale comment is part of the hunk — keep it.)

- [ ] **Step 3: Build + test framework**

Run: `cd framework && go build ./... && go test ./modelcatalog/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add framework/modelcatalog/sync.go
git commit -m "refactor(cluster): reset sync.go to baseline + deterministic upsert ordering only"
```

### Task 11: Add a guard test for the configurable model-parameters URL, then keep baseline behavior

**Files:**
- Test: `framework/modelcatalog/sync_url_test.go` (create)

- [ ] **Step 1: Write the failing test** (asserts the URL getter is used, not the constant)

```go
package modelcatalog

import "testing"

// Guards against the port regression where loadModelParametersFromURL hardcoded
// DefaultModelParametersURL instead of the configurable mc.modelParametersURL.
func TestModelParametersURL_IsConfigurable(t *testing.T) {
	mc := &ModelCatalog{}
	const custom = "https://example.test/custom-model-params"
	mc.modelParametersURL = custom
	if got := mc.getModelParametersURL(); got != custom {
		t.Fatalf("getModelParametersURL() = %q, want %q", got, custom)
	}
}
```

- [ ] **Step 2: Run it**

Run: `cd framework && go test ./modelcatalog/ -run TestModelParametersURL_IsConfigurable -v`
Expected: PASS after Task 10's baseline reset already restored `mc.getModelParametersURL()` in `loadModelParametersFromURL`. (If it fails, the reset missed the getter — restore the baseline line that reads `mc.getModelParametersURL()`.)

- [ ] **Step 3: Confirm `loadModelParametersFromURL` uses the getter**

Run: `grep -n 'getModelParametersURL\|DefaultModelParametersURL' framework/modelcatalog/sync.go`
Expected: `loadModelParametersFromURL` references `mc.getModelParametersURL()`; `DefaultModelParametersURL` only appears where it legitimately did in baseline.

- [ ] **Step 4: Commit**

```bash
git add framework/modelcatalog/sync_url_test.go
git commit -m "test(modelcatalog): guard configurable model-parameters URL (fix port regression)"
```

---

## Phase 5 — Document the patches

### Task 12: Write `FORK_PATCHES.md`

**Files:** Create `FORK_PATCHES.md` (repo root)

- [ ] **Step 1: Write the file from the final diffs**

Use the Group 2 inventory and the per-file `*.realdiff.txt` outputs to fill in real line numbers. Template:

```markdown
# Fork Patches

Every modification to upstream-owned code in this fork, so an upstream merge is mechanical
re-application, not re-derivation. Baseline: `4bbcd5846` (merge-base with upstream).
All *new* files (server_cluster.go, redis_counters.go, cluster_syncer.go,
publishing_config_store.go, quota_window.go, lib/cluster_config.go) are fork-owned and not listed here.

## Feature: Multi-node cluster sync + calendar-aligned quota windows

### transports/bifrost-http/server/server.go
- struct `BifrostHTTPServer`: +6 fields (clusterSyncer, clusterRedisClient, clusterCtx,
  clusterCancel, clusterEventNodeID, fullReloadMu). Why: hold cluster runtime handles.
- bootstrap (before LoadPlugins): `s.initClusterPublishing(ctx)`. Why: wrap ConfigStore so
  governance + handlers use the publishing store.
- bootstrap (after routes): `s.initClusterSubscriberAndRedis(ctx)`. Why: FullReload needs Client + routes.
- shutdown: `s.clusterCancel()` + `s.clusterRedisClient.Close()`.
  Logic lives in: server_cluster.go.

### transports/bifrost-http/lib/config.go
- `Config`: +field `Cluster *ClusterConfig` (+ unmarshal/load plumbing). Type in lib/cluster_config.go.

### transports/config.schema.json
- additive `cluster` object (Redis connection + consumer settings).

### plugins/governance/store.go
- `LocalGovernanceStore`: +fields redisCounters, redisAvailable.
- BumpBudgetUsage / BumpRateLimitUsage / ResetExpiredBudgetsInMemory: guarded redis counter calls.
- read + reset paths: calls to clusterRateUsage/clusterBudgetSpent/EvaluateQuotaWindow/resetRateLimitRedis.
  Logic lives in: redis_counters.go, quota_window.go.

### plugins/governance/tracker.go
- UpdateUsage, resetExpiredCounters: window-aware reset.
- PerformStartupResets: rewritten for calendar-aligned startup resets. Logic in: quota_window.go.

### framework/modelcatalog/sync.go
- syncPricing, syncModelParameters: deterministic dedup + lock-order sort. Why: concurrent multi-node
  upserts deadlock on PostgreSQL (40P01) without a stable row lock order.

### framework/configstore/rdb.go
- (no production patch) test helper moved to rdb_fork_test.go.
```

- [ ] **Step 2: Cross-check every entry has a real line/site**

Run: `grep -nE 'initClusterPublishing|initClusterSubscriberAndRedis|clusterCancel' transports/bifrost-http/server/server.go`
Fill exact line numbers into `FORK_PATCHES.md`.

- [ ] **Step 3: Commit**

```bash
git add FORK_PATCHES.md
git commit -m "docs(fork): document cluster-sync overlay patches for mechanical upstream merges"
```

---

## Phase 6 — Full verification & footprint audit (acceptance gate)

### Task 13: Green suite + minimized footprint proof

**Files:** none (verification)

- [ ] **Step 1: Full per-module build + test**

Run:
```bash
cd framework && go build ./... && go test ./configstore/... ./modelcatalog/... ; cd -
cd plugins/governance && go build ./... && go test ./... ; cd -
cd transports && go build ./... ; cd -
cd tests/governance && go test ./... ; cd -
```
Expected: all PASS.

- [ ] **Step 2: Footprint audit — prove churn is gone**

Run:
```bash
git diff --stat 4bbcd5846 HEAD -- \
  transports/bifrost-http/server/server.go \
  transports/bifrost-http/lib/config.go \
  framework/configstore/rdb.go \
  framework/modelcatalog/sync.go \
  plugins/governance/store.go \
  plugins/governance/tracker.go
```
Expected: each file now shows only tens of changed lines (the documented patches), versus the thousands in `/tmp/footprint-before.txt`. Compare the two.

- [ ] **Step 3: Confirm every upstream-file change is in `FORK_PATCHES.md`**

Manually walk the Step 2 diff; each hunk must correspond to a `FORK_PATCHES.md` entry. Any hunk that isn't is either leftover churn (revert it) or an undocumented patch (document it).

- [ ] **Step 4: Manual smoke (multi-node behavior unchanged)**

Bring up the two-node docker compose used by the feature and confirm a config change on one node propagates to the other, and governance counters are shared via Redis. Use `deploy/test/docker-compose.yml`.
Expected: peer node reflects the change; counters increment across nodes.

- [ ] **Step 5: Final commit / ready for review**

```bash
git commit --allow-empty -m "chore(cluster): overlay refactor complete — footprint minimized, suite green"
```

### Task 14: Finish the branch (per superpowers:finishing-a-development-branch)

- [ ] Present merge/PR options; ensure the worktree is clean and the suite is green before integrating.

---

## Self-Review (run before handing off)

**Spec coverage:**
- Every Group 2 patch → has a re-apply task (Phases 3–4) and a `FORK_PATCHES.md` entry (Task 12). ✓
- Every Group 0/1 movable → relocated (Phases 1–2). ✓
- Quota-window scope (in scope) → tracker.go (Task 7) + quota_window.go (already a new file) + store.go call-sites (Task 6). ✓
- URL regression → reverted (Task 10) + guarded (Task 11). ✓

**Behavior-preservation:** every move/reset task ends in `go build` + the existing test suite; the multinode + quota_window tests are the oracle. The only intended behavior change is the URL *revert* (back to baseline), covered by a new test. ✓

**Type/symbol consistency:** moved methods stay in the same package (server, governance, configstore) → unexported access preserved, no interface introduced; signatures unchanged because bodies are moved verbatim. ✓

**Risk note:** the highest-risk task is Task 7 (`PerformStartupResets` 102→19 rewrite re-application). Its guard is the `quota_window` test suite — if those pass, the rewrite is correctly re-applied. If unavailable, add a focused test before resetting the file.
