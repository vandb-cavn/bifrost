# Fork Overlay Implementation Playbook

**Context:** This repo is a **fork of open-source Bifrost** that adds enterprise-style features (multi-user, RBAC, governance, …). We pull updates from upstream and want merges to stay clean. This playbook captures the strategy we used for **Phase 1 (Identity Foundation)** so later phases (2–7 of the Option-B roadmap) can reuse it.

**One-line philosophy:** *Put new behavior in a fork-owned package; let core "call in" through a few stable, documented hooks. Trade a little indirection for low, mechanical upstream merges.*

---

## 1. The end-to-end lifecycle we follow per phase

```
roadmap pillar
   │  (challenge it against the market before building)
   ▼
SPEC  (behavior-only contract)  ──► self-review against the REAL code
   │                                  └─ verify every code-grounded claim; hunt security holes
   ▼
ARCHITECTURE decision (native vs overlay)  ──► map the seams first
   │
   ▼
PLAN  (TDD, task-by-task, real signatures) ──► self-review against the REAL code
   │
   ▼
IMPLEMENT in a git worktree, one task = one commit, tests first
   │
   ▼
VERIFY  (full suite + manual smoke + "core footprint" audit)
```

Each arrow that says **"self-review against the real code"** is non-negotiable — it is where we caught a privilege-escalation hole and several compile-breakers (see §6).

---

## 2. The overlay architecture pattern (the core technique)

**Goal:** implement a cross-cutting feature while editing as few upstream files as possible.

**Shape:**
- A new **fork-owned package** holds *all* logic: its own tables, migration, middleware, handlers, store.
- Core "calls in" through **minimal stable hooks** (ideally appends at a bootstrap point).
- Every upstream edit is recorded in **`FORK_PATCHES.md`** so a rebase is mechanical re-application, not re-derivation.

**Phase 1 result:** ~11 core-file edits (native plan) → **3 documented fork patches** (2 bootstrap lines + 1 serialization patch + UI). Everything else = new files.

**When to use overlay vs native:**
| Situation | Choose |
|-----------|--------|
| Fork that syncs upstream often / fears merge conflicts on hot files | **Overlay** |
| You own upstream, or sync rarely, or want to contribute back | **Native** |
| Feature is cross-cutting over *core* routes/flows (auth, RBAC, logging) | Overlay needs ≥1 hook — accept it |

**The honest cost of overlay** (tell the team every time): extra indirection, possibly +1 query, sometimes a parallel table or an "interceptor" that feels magic, and one or two features (output transforms like key-masking) that still need a small core patch.

---

## 3. Seam-mapping checklist — do this BEFORE deciding how invasive to be

Before writing a plan, find what the core already exposes so you can ride existing seams instead of cutting new ones. For Bifrost we found:

- [ ] **Migrations:** `ConfigStore.RunMigration(ctx, fn)` / `RunSingleMigration` — explicitly for "downstream consumers." Run your migration with **0 core edits**.
- [ ] **Routes:** is the router a public field? (`s.Router`) → register your routes from your package, **0 core edits**.
- [ ] **Middleware chain:** where is the cross-cutting chain assembled? (`apiMiddlewares` in bootstrap) → this is the **one irreducible hook** for anything that must wrap *core* routes (auth, RBAC).
- [ ] **Runtime DB handle:** can you get `*gorm.DB`? (type-assert `ConfigStore`→`*RDBConfigStore`, `.DB()`).
- [ ] **Existing extension model:** is there a flag/branch the vendor uses for their own overlay? (`IsEnterprise`). **Study it for the pattern, but do NOT hijack it** if it belongs to the upstream's proprietary edition.
- [ ] **Public structs/interfaces:** what's exported on the server (`Router`, `Config`, `Ctx`, …) that your hook can reach?
- [ ] **Import-cycle check:** your package must depend on core, not vice-versa. Pass narrow deps (router, store, callbacks) into your `Wire(...)`/`Middlewares(...)` — don't import the `server` package from your overlay.

**Rule of thumb:** the only edits you can't avoid are where core must *wrap your code around its own behavior* (the middleware chain). Everything else (storage, routes, migration, even login override via an interceptor) can usually be 0-edit.

---

## 4. Overlay wiring recipe (reusable skeleton)

```
yourfeature/
  tables.go      → own tables (never alter core tables; use a mapping table if you need to link to a core row)
  store.go       → persistence over *gorm.DB (own narrow interface; do NOT extend the core ConfigStore interface)
  migration.go   → create tables + data migration; idempotent; run via ConfigStore.RunMigration
  middleware.go  → cross-cutting behavior; intercept specific routes here instead of editing core handlers
  handlers.go    → your new endpoints
  wire.go        → Middlewares(deps) []Middleware   +   Wire(ctx, router, store) error
```

**Core hook (the documented fork patch):**
```go
// bootstrap, in the OSS path — append your middlewares to the chain core applies to all /api/* routes
apiMiddlewares = append(apiMiddlewares, yourfeature.Middlewares(store, deps)...)
...
// after core routes are registered
yourfeature.Wire(ctx, s.Router, s.Config.ConfigStore)
```

**Tricks that kept Phase 1 at 0 extra core edits:**
- **Login override** → an interceptor middleware short-circuits the route and serves the new behavior; the core handler is never reached (no `session.go` edit, no double-path).
- **Session→user link without touching core sessions** → own mapping table keyed by the core token hash; backfill it in the migration so pre-upgrade sessions still resolve (nobody gets logged out).
- **No interface surgery** → define your *own* narrow interface; add methods on the concrete store in a new file. Avoids breaking every implementer (incl. upstream's test mocks).
- **Own context keys** → declare them in your package, not in `core/schemas`.

**The one edit you usually can't avoid:** transforming *core handler output* (e.g. masking secrets in a provider response). Accept it as a small, documented patch — building a response-rewriting middleware is usually more fragile than the patch.

---

## 5. Spec hygiene — keep the spec implementation-agnostic

A spec that bakes in HOW (table names, function signatures, file line numbers) goes stale the moment the architecture changes. We rewrote Phase 1's spec to be **behavior-only**:

- Describe **what** must be true ("a session resolves to exactly one active user"), not **where** it's stored.
- Put concrete tables/columns/functions/line-numbers in the **plan**, not the spec.
- Keep in the spec: objective, API shapes, role/permission rules, business rules, error codes, acceptance criteria.
- Add an **"Implementation approach"** note at the top pointing to the plan as the authoritative *how*.
- **Exception worth keeping:** security *rationale* citations (e.g. "`/api/config` is admin-only because its handler writes `AuthConfig`"). That's evidence for a non-obvious decision, not a HOW prescription.

Benefit: the same spec survived the native→overlay pivot with **zero changes to behavior/acceptance** — only the leaked HOW sections needed rewriting.

---

## 6. Self-review discipline — the highest-ROI habit

After writing a spec or plan, **re-open the actual code and verify every claim.** This caught, in Phase 1:

- 🔴 **A privilege-escalation hole:** the permission map listed `/api/config` as operator-writable, but its handler writes `AuthConfig` (auth on/off, admin creds). An operator could disable auth. → fixed to admin-only **+ added a fail-closed default** for unmapped routes so the *next* such trap is denied, not leaked. **Lesson: never trust a route-name→permission guess; read the handler.**
- 🟠 **Interface blast radius:** adding methods to the shared `ConfigStore` would break a second implementer (a test mock). → overlay uses its own interface. **Lesson: grep for every implementer before touching a shared interface.**
- 🟠 **Unverified DB coercion:** scanning a TEXT column into `int` had no precedent in the codebase. → use the established string-scan + parse. **Lesson: match existing conventions; "probably works" isn't verification.**
- 🟠 **Cross-task signature drift:** a constructor changed shape between two tasks. **Lesson: check types/signatures are consistent across tasks.**

**Checklist for any self-review:** (1) does every referenced symbol/line still exist? (2) does the route→permission mapping match what the handler actually does? (3) who else implements an interface you're changing? (4) are you following an existing convention or inventing one? (5) do later tasks match earlier signatures?

---

## 7. Per-phase quick checklist (copy this for Phase 2+)

- [ ] **Challenge the roadmap pillar** against the market; adjust scope before building.
- [ ] **Write a behavior-only spec**; self-review it against the real code (security pass included).
- [ ] **Map the seams** (§3) before choosing architecture.
- [ ] **Default to overlay** (this is a fork): new package + `FORK_PATCHES.md`; aim for ≤3 documented core patches.
- [ ] **Write a TDD plan** grounded in verified signatures; self-review against code.
- [ ] **Implement in a git worktree**, one task = one commit, tests first, `go build ./...` after each task.
- [ ] **Don't touch `IsEnterprise`** or any upstream-proprietary seam.
- [ ] **Verify:** full suite + manual upgrade smoke + a `git diff --stat` audit proving the core footprint = exactly the documented patches.
- [ ] **Update `FORK_PATCHES.md`** so the next upstream merge is mechanical.

---

## 8. Anti-patterns we explicitly rejected

- Editing hot, security-critical core files (`middlewares.go`, `session.go`, `server.go`, `rdb.go`) in many places → recurring merge tax + risk of merge-induced auth bugs.
- Extending the shared `ConfigStore` interface → ripples to every implementer.
- Adding columns to core tables → schema coupling; use an own mapping table instead.
- Hijacking the upstream `IsEnterprise` flag → breaks on sync.
- Leaving the legacy login path alive alongside the new one → a bypass of the new model (we close it via interceptor).
- Letting unmapped routes default to "allowed" → fail **closed** instead.
