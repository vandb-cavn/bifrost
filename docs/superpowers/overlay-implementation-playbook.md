# Fork Overlay Implementation Playbook

**What this is:** a reusable strategy for adding and evolving features in a repo that is a **fork of an upstream open-source project** which you keep syncing from. It applies to *any* such fork and *every* feature you build on it — not one project or one phase. The worked examples come from one fork (Bifrost, adding enterprise features), but the prescriptions are project-agnostic; treat the `Example:` callouts as evidence, not requirements.

**When it applies:**
- You don't own upstream, you pull updates from it, and you want those merges to stay cheap.
- You are adding behavior the upstream doesn't have (and may never accept), or changing how existing behavior works for your edition.

**When it does *not* apply (use plain native development instead):**
- You own upstream, sync rarely, or intend to contribute the change back. Then edit core directly — overlay's indirection isn't worth it.

**One-line philosophy:** *Put new behavior in a fork-owned package (or a fork-owned file); let core "call in" through a few stable, documented hooks. The goal is **minimal footprint on upstream-owned lines**, not overlay purity — trade a little indirection for low, mechanical upstream merges.*

---

## 1. The end-to-end lifecycle (run this per feature/change)

```
feature idea / roadmap item
   │  (challenge it against the market & the upstream's direction before building)
   ▼
SPEC  (behavior-only contract)  ──► self-review against the REAL code
   │                                  └─ verify every code-grounded claim; hunt security holes
   ▼
ARCHITECTURE decision (native vs overlay)  ──► map the seams first (§3)
   │
   ▼
PLAN  (TDD, task-by-task, real signatures) ──► self-review against the REAL code
   │
   ▼
IMPLEMENT in a git worktree, one task = one commit, tests first
   │
   ▼
VERIFY  (full suite + manual smoke + "upstream footprint" audit)
```

Each arrow that says **"self-review against the real code"** is non-negotiable — it is where guesses get caught before they ship (see §6 for the privilege-escalation hole that this step caught).

---

## 2. The overlay architecture pattern (the core technique)

**Goal:** implement a cross-cutting feature while modifying as few upstream-owned lines as possible.

**Shape:**
- A new **fork-owned package** holds *all* the new logic: its own tables, migration, middleware, handlers, store.
- Core "calls in" through **minimal stable hooks** (ideally appends at a single bootstrap point).
- Every upstream edit is recorded in a **`FORK_PATCHES.md`** so a rebase is mechanical re-application, not re-derivation.

> **Example (Bifrost, identity feature):** the naive native plan touched ~11 core files. The overlay version shipped as **3 documented fork patches** (2 bootstrap lines + 1 output-serialization patch + UI). Everything else was new files.

**When to use overlay vs native:**
| Situation | Choose |
|-----------|--------|
| Fork that syncs upstream often / fears merge conflicts on hot files | **Overlay** |
| You own upstream, sync rarely, or want to contribute back | **Native** |
| Feature is cross-cutting over *core* routes/flows (auth, RBAC, logging, config-sync) | Overlay needs ≥1 hook — accept it |

**The honest cost of overlay** (tell the team every time): extra indirection, possibly +1 query, sometimes a parallel table or an "interceptor" that feels magic, and a few features (output transforms, hot-path counters) that still need a small documented patch.

### 2.1 The pragmatic rule: minimize intervention, don't chase purity

**100% overlay is not the goal — minimizing footprint on upstream-owned lines is.** The test is not *"did we avoid every core file?"* but *"does each core file hold a thin **call into our package**, not the logic itself?"* Write the logic in a fork-owned package (or a fork-owned file inside a core package); let core call one function.

This works because **merge tax comes from modifying lines upstream already owns — not from adding new ones.** Version control merges new files and new methods cleanly; conflicts come from (a) editing the body of an existing upstream function, and (b) inserting fields into the middle of an upstream struct/class. So classify *every* change by that axis, not by "is it in a core file":

| Change | Merge tax | Verdict |
|--------|-----------|---------|
| New file in your fork-owned package | ~0 | Free — host as much logic here as possible |
| New method on a core type, placed in a **new file of the same package/module** | ~0 | Free — use this for orchestration that needs *unexported/private* core methods |
| New field on a core struct/class | small | Acceptable; document it |
| One thin call appended at a bootstrap/hook point | small | The irreducible hook — accept it |
| Edit to the **body of an existing** upstream function | **real** | Minimize; document each in `FORK_PATCHES.md` |
| Logic **interleaved** through an existing hot path (e.g. counter writes inside a budget loop) | **real, unavoidable for some features** | Keep the *client/helper* in your package; accept the thin guarded call-sites as documented patches |

**Consequence:** a large orchestration method that calls many *private* core methods does **not** need a fat interface to "earn" overlay status, and does **not** belong inlined in the hot upstream file. Put it in a **new file of the same package/module**. Languages that let a type's methods span files (Go, C# `partial`, Ruby/Python reopening, Kotlin extensions) make that file fork-owned with **0 merge conflict** while still reaching private members — better than both inlining it and building an interface just for purity. Reserve narrow interfaces (§3 import-cycle check) for when your code must live in a *separate* package.

> **Example (Bifrost, cluster sync):** a ~600-line `FullReload` that calls ~18 unexported server methods went into a new `server_cluster.go` in the same package — 0 conflict — instead of an 18-method interface.

**Rule of thumb for "good enough":** every surviving core edit is either (1) a new file, (2) a new method/field, or (3) a documented thin call/guard in `FORK_PATCHES.md`. If you can say that, stop — you've minimized intervention. Don't spend a day building abstractions to delete the last three documented patch lines.

---

## 3. Seam-mapping checklist — do this BEFORE deciding how invasive to be

Before writing a plan, find what the core already exposes so you can ride existing seams instead of cutting new ones. Look for each of these categories in *your* codebase:

- [ ] **Migrations:** does the core offer a migration hook for downstream consumers? Run your schema changes through it with **0 core edits**. *(Bifrost: `ConfigStore.RunMigration` / `RunSingleMigration`.)*
- [ ] **Routes:** is the router a public field/handle you can register on from your package? → **0 core edits**. *(Bifrost: `s.Router`.)*
- [ ] **Cross-cutting chain:** where is the middleware/interceptor/filter chain assembled? This is usually the **one irreducible hook** for anything that must wrap *core* routes/flows (auth, RBAC, logging). *(Bifrost: `apiMiddlewares` in bootstrap.)*
- [ ] **Runtime data handle:** can you reach the live DB/connection/registry? *(Bifrost: type-assert `ConfigStore`→concrete store, call `.DB()`.)*
- [ ] **Existing extension model:** is there a flag/branch the vendor uses for their *own* edition? **Study it for the pattern, but do NOT hijack it** if it belongs to the upstream's proprietary edition — it will fight you on every sync. *(Bifrost: `IsEnterprise`.)*
- [ ] **Public surface:** what types/fields/interfaces are exported that your hook can reach without new exports?
- [ ] **Import-cycle / dependency-direction check:** your package must depend on core, not vice-versa. Pass narrow deps (router, store, callbacks) *into* your `Wire(...)` / `Middlewares(...)` entrypoint — don't make your overlay import the core's top-level package.

**Rule of thumb:** the only edits you usually can't avoid are where core must *wrap your code around its own behavior* (the cross-cutting chain) or *transform its own output*. Everything else — storage, routes, migration, even overriding a built-in flow via an interceptor — can usually be 0-edit.

---

## 4. Overlay wiring recipe (reusable skeleton)

```
yourfeature/
  tables.go      → own tables (never alter core tables; use a mapping table to link to a core row)
  store.go       → persistence over the runtime DB handle (own narrow interface; do NOT extend a shared core interface)
  migration.go   → create tables + data migration; idempotent; run via the core migration hook
  middleware.go  → cross-cutting behavior; intercept specific routes here instead of editing core handlers
  handlers.go    → your new endpoints
  wire.go        → Middlewares(deps) []Middleware   +   Wire(ctx, router, store) error
```

**Core hook (the documented fork patch) — append at the bootstrap seam:**
```go
// in the upstream bootstrap path — append your middlewares to the chain core applies to all routes
apiMiddlewares = append(apiMiddlewares, yourfeature.Middlewares(store, deps)...)
...
// after core routes are registered
yourfeature.Wire(ctx, router, store)
```

**Techniques that keep the footprint near zero:**
- **Override a built-in flow without editing it** → an interceptor middleware short-circuits the route and serves the new behavior; the core handler is never reached (no edit to the core handler, no double-path). *(Bifrost: login override, no `session.go` edit.)*
- **Link to a core row without touching core tables** → own mapping table keyed by a stable core identifier; backfill it in the migration so pre-upgrade data still resolves (nobody gets logged out / loses links). *(Bifrost: session→user mapping keyed by token hash.)*
- **No shared-interface surgery** → define your *own* narrow interface; add methods on the concrete type in a new file. Avoids breaking every implementer (including upstream's test mocks).
- **Own your context keys / globals** → declare them in your package, not in a core shared package.
- **Orchestration that needs core internals** → don't build a fat interface and don't inline it into the hot file. Put it in a **new file of the same package/module**; a type's methods may span files, so your fork-owned file reaches private core members with **0 merge conflict** (see §2.1).

**The edits you usually *can't* avoid (accept them as small, documented patches):**
- **Transforming core handler output** (e.g. masking secrets in a response) — a response-rewriting middleware is usually more fragile than the patch.
- **Hot-path hooks interleaved with core logic** (e.g. writing a shared counter inside a budget/rate-limit loop) — keep the client in your package; the call-sites stay as thin guarded inserts.

---

## 5. Spec hygiene — keep the spec implementation-agnostic

A spec that bakes in HOW (table names, function signatures, file line numbers) goes stale the moment the architecture changes — and in a fork, the architecture *will* shift between native and overlay. Keep specs **behavior-only**:

- Describe **what** must be true ("a session resolves to exactly one active user"), not **where** it's stored.
- Put concrete tables/columns/functions/line-numbers in the **plan**, not the spec.
- Keep in the spec: objective, API shapes, role/permission rules, business rules, error codes, acceptance criteria.
- Add an **"Implementation approach"** note at the top pointing to the plan as the authoritative *how*.
- **Exception worth keeping:** security *rationale* citations (e.g. "`/api/config` is admin-only because its handler writes auth credentials"). That's evidence for a non-obvious decision, not a HOW prescription.

> **Payoff (Bifrost):** the same spec survived the native→overlay pivot with **zero changes to behavior/acceptance** — only the leaked HOW sections needed rewriting.

---

## 6. Self-review discipline — the highest-ROI habit

After writing a spec or plan, **re-open the actual code and verify every claim.** Real examples this step caught:

- 🔴 **A privilege-escalation hole:** a permission map listed an endpoint as operator-writable, but its handler actually writes auth config (auth on/off, admin creds). An operator could disable auth. → fixed to admin-only **+ added a fail-closed default** for unmapped routes so the *next* such trap is denied, not leaked. **Lesson: never trust a route-name→permission guess; read the handler.**
- 🟠 **Interface blast radius:** adding methods to a shared store interface would break a second implementer (a test mock). → use your own interface. **Lesson: grep for every implementer before touching a shared interface.**
- 🟠 **Unverified data coercion:** scanning a TEXT column into an `int` had no precedent in the codebase. → match the established string-scan + parse. **Lesson: follow existing conventions; "probably works" isn't verification.**
- 🟠 **Cross-task signature drift:** a constructor changed shape between two tasks. **Lesson: check types/signatures are consistent across tasks.**

**Checklist for any self-review:** (1) does every referenced symbol/line still exist? (2) does the route→permission mapping match what the handler actually does? (3) who else implements an interface you're changing? (4) are you following an existing convention or inventing one? (5) do later tasks match earlier signatures?

---

## 7. Per-feature quick checklist (copy this for every feature)

- [ ] **Challenge the feature** against the market *and* the upstream's direction; adjust scope before building.
- [ ] **Write a behavior-only spec**; self-review it against the real code (security pass included).
- [ ] **Map the seams** (§3) before choosing architecture.
- [ ] **Decide native vs overlay** (§2 table). On a fork that syncs upstream, default to overlay: new package + `FORK_PATCHES.md`. Aim to **minimize footprint on upstream-owned lines** (§2.1), not to hit a fixed patch count — every surviving core edit should be a new file, a new method/field, or a documented thin call/guard.
- [ ] **Write a TDD plan** grounded in verified signatures; self-review against code.
- [ ] **Implement in a git worktree**, one task = one commit, tests first, build the whole project after each task.
- [ ] **Don't touch the upstream's proprietary-edition seam** (flags like `IsEnterprise`) or any hot file you don't have to.
- [ ] **Verify:** full suite + manual upgrade smoke + a `git diff --stat` audit proving the upstream footprint = exactly the documented patches.
- [ ] **Update `FORK_PATCHES.md`** so the next upstream merge is mechanical.

---

## 8. Anti-patterns we explicitly reject

- **Editing the bodies of** hot, security-critical core files (auth/session/server/DB bootstrap) in many places → recurring merge tax + risk of merge-induced auth bugs. (Note the precise target: *adding* new methods/files to these packages is cheap and fine — see §2.1; the tax is in modifying functions upstream already owns.)
- **Conflating a feature with an upstream version bump in one commit** → re-derivation churn masquerades as a huge footprint and makes the real, small change impossible to audit. Rebase onto the new upstream first, *then* add the feature in a separate commit.
- **Extending a shared core interface** → ripples to every implementer (including upstream test mocks). Use your own narrow interface.
- **Adding columns to core tables** → schema coupling; use your own mapping table instead.
- **Hijacking the upstream's proprietary-edition flag** → breaks on every sync.
- **Leaving a legacy flow alive alongside the new one** → a silent bypass of the new model. Close it via an interceptor.
- **Letting unmapped routes/permissions default to "allowed"** → fail **closed** instead.
