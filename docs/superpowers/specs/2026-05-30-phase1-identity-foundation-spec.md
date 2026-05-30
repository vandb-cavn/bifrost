# Spec: Phase 1 — Identity Foundation (Multi-user + RBAC)

**Date:** 2026-05-30
**Phase:** 1 — Identity Foundation (roadmap Features 1.1 + 1.2)
**Scope:** Replace the single-admin model with a real multi-user system (named users with roles, identity migration from the legacy admin, user-management + auth-settings API), and gate every dashboard/API action by the requesting user's role (RBAC).
**Out of scope:** Team membership and per-team/row-level data scoping (Phase 2.1 / 2.4), Access Profiles (Phase 2.3), Guardrails (Phase 3), Agent identity (Phase 4), SSO (Phase 5), SCIM (Phase 6), Audit logs (Phase 7).

> **Implementation approach (read first):** This spec defines **behavior — the contract — only.** Section numbers, API shapes, role rules, and acceptance criteria are normative. The current implementation is a **fork-owned overlay package** (`transports/bifrost-http/identity/`) that delivers this behavior *without modifying* core auth/session code; the OSS core footprint is three documented "fork patches." All storage and wiring below are stated in **behavioral terms** (e.g. "a session resolves to an active user") rather than prescribing concrete tables, columns, or functions. The authoritative *how* is the plan: `docs/superpowers/plans/2026-05-30-phase1-identity-foundation-overlay.md`.

---

## 1. Objective & Target Users

**Objective:** Bifrost currently has exactly one admin (`admin_username` / `admin_password` in the `governance_config` key-value table) and sessions are not linked to any identity. This phase introduces named users with roles so that different people get different permissions — the prerequisite that unblocks every later enterprise feature.

**Who this is for:**
- **Startup/scaleup operators** who need to give teammates dashboard access without sharing one admin password.
- **The existing single admin**, whose login must keep working unchanged after upgrade (no forced logout, no manual migration step).

**Definition of done:** An admin can log in, create an `operator` and a `viewer`, and each new user can log in and is allowed exactly the actions their role permits — enforced server-side, verified by tests, with the legacy admin migrated silently on first startup.

This phase is split into two tightly-coupled parts. **Part A (Multi-user)** builds the identity substrate; roles are *stored* but not yet *enforced*. **Part B (RBAC)** turns those stored roles into enforced permissions. They ship together as Phase 1.

---

## 2. Commands (build / test / run)

Bifrost is a Go workspace (`go.work`) with a Next.js dashboard in `ui/`. Relevant commands:

```bash
# Build everything (catches any wiring breakage)
go build ./...

# Unit tests for the identity overlay + any touched handler tests
go test ./transports/bifrost-http/identity/...
go test ./transports/bifrost-http/handlers/...

# Migration sanity: behavior must hold on both dialects
#   SQLite  → default local/dev DB
#   Postgres → cluster / production DB

# UI (login form + role-aware controls)
cd ui && npm run build
```

No new build tooling is introduced. The work lives in a new overlay package plus a small set of documented core patches (see §8).

---

## 3. Part A — Multi-user System (Feature 1.1)

### 3.1 Data Model (logical)

The system introduces two logical concepts. *How* they are stored (which is owned by the overlay package, not core tables) is a plan concern; the contract is the fields and rules below.

#### User

| Field | Rule |
|-------|------|
| `id` | stable unique identifier (uuid) |
| `email` | login identity; **unique**, stored lowercase, trimmed |
| `name` | display name |
| `role` | one of `admin` / `operator` / `viewer` |
| `password_hash` | bcrypt hash; **optional** (absent = no local password, reserved for future SSO) |
| `is_active` | inactive users cannot log in and fail authorization |
| `last_login_at` | updated on each successful login |
| `created_at` / `updated_at` | timestamps |

- A password is **never** serialized in any API response.
- `role` is stored as one of the three fixed strings (no custom roles in Phase 1).

#### Session ↔ User association

Every authenticated session **resolves to exactly one active user.** A session that cannot be resolved to an active user is treated as unauthenticated (`401`).

- The association must work for sessions that already existed **before** the upgrade (the migrated admin's current session stays valid — see §3.2).
- No assumption is made about *where* the association is stored; the overlay maintains it without altering the core session record.

### 3.2 Migration (runs silently on first startup after upgrade)

The migration must be **automatic**, **idempotent**, and behave correctly on both SQLite and Postgres.

**When a legacy admin exists** (the single-admin credentials are present in `governance_config`):

1. Create a `User` for the legacy admin:
   - `email = "admin@localhost"` (placeholder; the admin can change it after login)
   - `name = ` the legacy admin username
   - `role = admin`, `is_active = true`
   - `password_hash = ` the **existing** stored hash, **copied verbatim** (see below)
2. Ensure the legacy admin's **already-active sessions resolve to this new user**, so they are **not** logged out by the upgrade.

**When no legacy admin exists** (auth disabled / fresh install): the user system is initialized empty; nothing to migrate.

**Copy the password hash — never re-hash.** The stored `admin_password` is *already* a bcrypt hash. Re-hashing would yield `bcrypt(bcrypt(pw))` and the next login would fail. Copy it as-is.

**Legacy keys are retained.** The original single-admin credentials are **not** deleted from `governance_config` — they remain as a safe rollback path; login simply stops reading them.

**Idempotency.** Re-running the migration must not create a duplicate admin or duplicate associations: seed the admin only if `admin@localhost` does not already exist.

**Bootstrapping with no admin (auth disabled):** while auth is disabled, the request is treated as a local admin and authorization is bypassed. The operator bootstraps in order: (auth still disabled) → create the first real admin via `POST /api/users` → then enable auth.

### 3.3 Session Behavior

**Authenticated request flow** (per request, behavioral):

```
1. Obtain the session token (cookie "token" or "Authorization: Bearer <token>")
2. Resolve the token to its session, then to the associated user
3. If it cannot be resolved to an ACTIVE user → 401
4. Make the resolved user available to authorization (RBAC, §4.3)
```

Role is **never cached in the session** — it is resolved fresh on every request. Cost: one extra indexed lookup per dashboard request (acceptable for an admin UI); the benefit is that role changes and deactivations take effect immediately, with no cluster cache to invalidate.

**Session invalidation rules:**

| Event | Action |
|-------|--------|
| User deactivated (`is_active = false`) | Do **not** delete sessions — next request 401s automatically |
| Role changed | Do **not** delete sessions — new role applies on next request |
| Admin resets another user's password | Delete **all** sessions for that user |
| User changes their own password | Delete all **other** sessions, keep the current one |
| Logout | Delete the current session |

**Session expiry** — stored in `governance_config`:

```
Key:   "session_expiry_hours"
Value: "720"  (default = 30 days, backward compatible)
Min:   1     Max: 8760 (1 year)
```

Applies to **new** sessions only; changing it does not retroactively shorten existing sessions. Admin-only.

Behavioral requirement: on login, the configured expiry must be applied **consistently** to both the session record and the auth cookie, so they never disagree. The default (720h) applies when unset.

### 3.4 Cluster Sync

**No new sync mechanism needed.** The cluster shares one database, so user records are already consistent across nodes. Because the user is resolved fresh on every request (no in-memory cache), a role change or deactivation on Node A applies on Node B at the next request. Session-expiry configuration is persisted in shared config, so all nodes observe the same value.

### 3.5 User Management API

| Method | Route | Min role | Description |
|--------|-------|----------|-------------|
| `GET`  | `/api/users` | admin | List all users |
| `POST` | `/api/users` | admin | Create a user |
| `GET`  | `/api/users/me` | any authenticated | Current user info |
| `GET`  | `/api/users/:id` | admin **or self** | Get one user |
| `PUT`  | `/api/users/:id` | admin **or self** | Update name, email |
| `PUT`  | `/api/users/:id/role` | admin (not own role) | Change role |
| `PUT`  | `/api/users/:id/password` | admin **or self** | Change/reset password |
| `PUT`  | `/api/users/:id/active` | admin (not self) | Activate / deactivate |

**No `DELETE`** — users are only deactivated (soft delete), to preserve future audit history.

`POST /api/users` body: `{ "email", "name", "role", "password" }`. Rules: unique email, password ≥ 8 chars, valid role. Store the password as a bcrypt hash (using the codebase's standard hasher).

`PUT /api/users/:id/password` has two modes:

| Caller | Body | Action |
|--------|------|--------|
| Admin resetting another user | `{ "new_password" }` | Hash, delete **all** that user's sessions |
| User changing own password | `{ "current_password", "new_password" }` | Verify the current password, then delete all **other** sessions |

Password verification distinguishes a **mismatch** (clean `false` → `401`) from an **internal error** (→ `500`, never surfaced as 401). `PUT /api/users/:id` (name/email) must **ignore** any `role` or `is_active` field in the body — those are only mutable via the dedicated `/role` and `/active` endpoints, so the email/name path cannot be used to bypass their guards.

`PUT /api/users/:id/active` body `{ "active": bool }`: cannot deactivate self; cannot deactivate the last admin.

### 3.6 Auth Settings API

```
GET /api/auth/settings   (admin)  → { "session_expiry_hours": 720, "is_auth_enabled": true }
PUT /api/auth/settings   (admin)  ← { "session_expiry_hours": 480 }
```

### 3.7 Login (changed)

`POST /api/session/login` — the `username` field becomes `email`:

```json
// before: { "username": "admin", "password": "..." }
// after:  { "email": "admin@localhost", "password": "..." }
```

4-step flow (behavioral):

```
1. Normalize email (lowercase, trim) → look up the user by email
2. Not found OR inactive OR no password set → 401 "invalid email or password"  (no enumeration leak)
3. bcrypt compare mismatch → the SAME 401 (a compare *error* is 500, not 401)
4. Create a session associated with the user (expiry = configured value);
   record last_login_at = now
```

Success response: `{ "message": "Login successful", "user": { id, email, name, role } }`.

**Breaking UI change:** the login form must send `email` instead of `username`.

> The legacy username-based login behavior must not remain reachable as an alternate path that bypasses the new user/RBAC model. (The overlay achieves this by intercepting the login route; see the plan.)

### 3.8 Business Rules & Error Handling (Part A)

**"Last admin"** = the only user with `role = 'admin'` AND `is_active = true`. Guards that touch the last admin must be **concurrency-safe**: the check ("is this the last active admin?") and the demote/deactivate write must be serialized (e.g. a transaction with a row lock) so two concurrent requests cannot both pass and leave the system with zero admins.

| Condition | HTTP | Message |
|-----------|------|---------|
| Email already exists | `409` | "email already in use" |
| Deactivate the last admin | `400` | "cannot deactivate the last admin" |
| Deactivate yourself | `400` | "cannot deactivate yourself" |
| Change your own role | `400` | "cannot change your own role" |
| Demote the last admin | `400` | "cannot remove the last admin" |
| Wrong `current_password` | `401` | "current password is incorrect" |
| Password < 8 chars | `400` | "password must be at least 8 characters" |
| Invalid role value | `400` | "role must be one of: admin, operator, viewer" |

Email: lowercase + trim; must contain `@` and be non-empty. The password hash must never appear in any JSON response. Errors follow the existing response shape: `{ "error": "..." }`.

---

## 4. Part B — Role-Based Access Control (Feature 1.2)

> **Phase boundary (important):** The roadmap's Feature 1.2 lists some rules that depend on **team membership** ("operator can only see teams they are a member of", "operator can see keys they created"). Team membership does not exist until **Phase 2.1**, and row-level data scoping is **Phase 2.4**. Therefore **Phase 1 RBAC is pure role-gating** — it decides *whether* a role may perform an action on a resource type, not *which rows* it may see. Data scoping is explicitly deferred to Phase 2. See Open Questions §7.

### 4.1 Role Definitions

| Role | Rank | Description |
|------|------|-------------|
| `admin` | 3 | Full access, including user management and auth/security settings |
| `operator` | 2 | Manage providers, API keys, virtual keys, teams, governance, MCP, plugins. **Cannot** manage users or change auth/security settings |
| `viewer` | 1 | Read-only on all resources. Cannot mutate anything. Cannot see raw API key values (masked) |

Hierarchy is strict and inheriting: rank ≥ required-rank passes. `admin` (3) ⊇ `operator` (2) ⊇ `viewer` (1).

### 4.2 Permission Model

A permission is `(resource_group, action) → minimum role`. Action is derived from HTTP method:

- **read** = `GET` → requires `viewer+`
- **mutate** = `POST` / `PUT` / `PATCH` / `DELETE` → requires `operator+` by default

Two resource groups override the default and require **admin** for *all* actions (including reads):

- **user management** — `/api/users*` (except `GET /api/users/me`, which is any authenticated user, and self-access rules in §3.5)
- **auth & security settings** — `/api/auth/settings`, and `/api/scim*` (Phase 6 surface, admin-gated now)

**Route → permission map** (by `/api/*` prefix; reads = `viewer+`, mutations = `operator+` unless noted):

| Route / prefix | Read | Mutate | Notes |
|----------------|------|--------|-------|
| `GET /api/users/me` | any auth | — | current user; **not** admin-gated |
| `GET/PUT /api/users/:id`, `PUT /api/users/:id/password` | self **or** admin | self **or** admin | middleware lets any authenticated user **reach** these; handler enforces self-vs-admin (§3.5). Must be matched *before* the admin gate below |
| `GET /api/users` (list), `POST /api/users`, `PUT /api/users/:id/role`, `PUT /api/users/:id/active` | admin | admin | strict admin |
| `/api/auth` | admin | admin | session expiry, security settings |
| `/api/config` | **admin** | **admin** | ⚠️ `updateConfig` writes `AuthConfig` (`is_enabled`, admin creds) — auth/security group, **never** operator (see [config.go:239,248](../../../transports/bifrost-http/handlers/config.go)) |
| `/api/scim` | admin | admin | Phase 6 surface; admin-gated **except** the public `/api/scim/oauth/*` callbacks already in `systemWhitelistedRoutes` |
| `/api/providers` | viewer | operator | |
| `/api/keys` | viewer | operator | raw key values masked for viewer (§4.5) |
| `/api/governance` | viewer | operator | teams, customers, virtual keys, budgets |
| `/api/mcp`, `/api/mcp-logs` | viewer | operator | MCP config; logs are read-only |
| `/api/plugins` | viewer | operator | |
| `/api/proxy-config`, `/api/cache` | viewer | operator | outbound network proxy + cache config — **verified** not auth/security (`GlobalProxyConfig`, no `AuthConfig`); proxy password already redacted on GET for all roles |
| `/api/models`, `/api/pricing`, `/api/prompt-repo`, `/api/feature-flags` | viewer | operator | |
| `/api/oauth` | viewer | operator | provider OAuth config (per-user temp-token paths keep their own auth) |
| `/api/logs` | viewer | — | read-only resource |
| `/api/session/logout`, `/api/session/ws-ticket` | any authenticated | | |

**Public (no auth, unchanged):** `/api/session/login`, `/api/session/is-auth-enabled`, `/api/version`, `/health`, `/api/oauth/callback`, login assets. These remain in the existing `systemWhitelistedRoutes`.

**Inference is exempt from role gating.** `/v1/chat/completions` and equivalents are governed by **virtual key** permissions (the governance plugin), *not* by user role. A `viewer` user holding a valid virtual key can still make inference calls. RBAC applies only to `/api/*` management routes, never to the inference path.

### 4.3 Enforcement Architecture

Authorization is a **server-side layer that runs after authentication** has resolved the request to an active user (§3.3). It is authoritative; the UI's hiding/disabling of controls (§4.6) is convenience only.

```
request
  → authentication        (existing: validate session)
  → resolve user          (resolve token → active user; make it available to authorization)
  → authorization (RBAC)  (look up required role for route+method; compare; 403 if insufficient)
  → handler
```

Mechanics (behavioral):

1. **Bypass when auth is disabled or the request is the local admin.** When the platform marks a request as local-admin (auth disabled / bootstrap), authorization is skipped (treated as admin). This preserves the auth-disabled and bootstrap flows and prevents a UI logout/redirect loop.
2. **Skip public routes.** Public/whitelisted routes (login, health, version, OAuth callback) are never gated.
3. **Match self-accessible user routes first.** `GET /api/users/me` and the `:id` self-routes (§4.2) are matched *before* the strict-admin `/api/users` rule and let any authenticated user reach the handler, which then enforces self-vs-admin (§3.5). Without this ordering the admin gate would `403` a user reading their own record.
4. **Resolve required role.** Match the remaining path prefix + method against the permission map (§4.2). **Unknown `/api/*` routes fail closed: mutations require `admin`, reads require `operator`.** This guarantees a new security-sensitive endpoint is *denied* (not leaked) if someone forgets to map it; intentionally operator/viewer-accessible routes must be added to the map explicitly. (This is exactly the trap that made an unmapped `/api/config` operator-writable in review — see §4.2.)
5. **Compare.** If the resolved user's rank `< required`, return `403`. Otherwise continue.

The resolved user is carried to the authorization layer via a request-context value. (In the overlay implementation this key lives in the overlay package, not in core — no core schema change.)

### 4.4 403 Behavior

When a role check fails, return `403 Forbidden` with a human-readable message naming the required role:

```json
{ "error": "forbidden: this action requires the 'operator' role or higher" }
```

This is distinct from Phase 2.4's `404`-on-out-of-scope behavior. In Phase 1 there is no row scoping, so a permission failure is always a clean `403` (the resource's existence is not a secret at the role level).

### 4.5 Raw API Key Visibility

`viewer` must never receive raw API key / secret values; they are masked in responses (e.g. `sk-…last4`). `operator` and `admin` receive full values. This is enforced at **serialization time** for key-bearing responses (providers, keys, virtual keys), not only by route gating — because a `viewer` *can* read these resources, just not their secrets.

> Per-creator visibility ("operator sees only keys they created") requires a `created_by` column and is **deferred to Phase 2.4** with the rest of row scoping. In Phase 1, operator/admin see full key values for all keys they can read.

### 4.6 UI Gating

The dashboard hides or disables controls the current user's role cannot use (read `role` from `GET /api/users/me`). This is purely a UX convenience — every check is independently enforced by the server-side authorization layer regardless of what the UI sends.

### 4.7 Business Rules & Errors (Part B)

| Condition | HTTP | Message |
|-----------|------|---------|
| Authenticated user lacks required role | `403` | "forbidden: this action requires the '<role>' role or higher" |
| Operator/viewer hits `/api/users*` (non-self) | `403` | "forbidden: this action requires the 'admin' role or higher" |
| Viewer attempts any mutation | `403` | "forbidden: this action requires the 'operator' role or higher" |

---

## 5. Testing Strategy

Test-first where practical (`superpowers:test-driven-development`). Coverage targets:

**Part A — Multi-user (identity store + handlers)**
- **Migration, legacy admin present:** seed the legacy single-admin credentials with a known bcrypt hash → run migration → assert one `admin` user exists, the hash is copied verbatim (login with the original password succeeds — proves no double-hash), and a pre-existing session still resolves to the migrated admin (not logged out).
- **Migration, no legacy admin:** user system initializes empty; system boots; idempotent on re-run (no duplicate admin).
- **Login:** correct email+password → 200 + user payload + an authenticated session; wrong password and unknown email return the **same** 401 (no enumeration); inactive user → 401; the legacy username login path is not reachable as a bypass.
- **Session flow:** a valid session resolves to a fresh role; deactivating a user mid-session → next request 401 without explicitly deleting the session; a role change is visible on the next request.
- **Guards (concurrency):** two concurrent "deactivate last admin" / "demote last admin" requests → exactly one succeeds, system always retains ≥1 active admin (validates the concurrency-safe guard from §3.8).
- **Password flows:** admin reset deletes all target sessions; self-change with wrong `current_password` → 401; self-change deletes other sessions but keeps current.

**Part B — RBAC (authorization layer)**
- **Role matrix test:** parametrize over {admin, operator, viewer} × representative routes/methods, asserting the exact allow/deny from §4.2. Minimum cases from the roadmap success criteria:
  - operator → `GET/POST /api/users` ⇒ `403`.
  - viewer → any `POST/PUT/DELETE` ⇒ `403`.
  - viewer → `GET` resource ⇒ `200`.
  - operator → mutate providers/keys/governance ⇒ allowed.
  - **operator → `PUT /api/config` ⇒ `403`** (regression guard: this is the auth/security escalation found in review — an operator must not be able to disable auth or change admin creds).
  - **operator → an unmapped `/api/*` mutation ⇒ `403`** (fail-closed default).
  - operator/viewer → `GET /api/users/me` and self `GET/PUT /api/users/:id` ⇒ `200` (self-access carve-out reaches the handler, not blocked by the admin gate).
- **Server-side enforcement:** deny holds even when the request bypasses the UI (raw HTTP), proving checks are not UI-dependent.
- **Auth-disabled bypass:** with `IsEnabled = false`, every route is reachable (IsLocalAdmin path), no 403.
- **Key masking:** viewer reads a provider/key resource → secret fields masked; operator/admin → full value.
- **Inference exemption:** viewer with a valid virtual key → inference call succeeds (not gated by RBAC).

**Manual / integration smoke:** fresh upgrade from a single-admin instance → existing admin still logged in (no forced logout), can create an operator and a viewer, each logs in and sees the correct gated UI.

---

## 6. Boundaries

**Always**
- Keep the existing admin logged in across upgrade; migrate silently (no manual step).
- Enforce every permission in the server-side authorization layer; treat UI gating as cosmetic.
- Resolve role fresh on each request (no role caching in sessions).
- Store passwords as bcrypt hashes; never log or serialize a password hash.
- Follow existing codebase conventions for response shape, migrations, and request-context values.
- Keep the last-admin guard concurrency-safe (serialize check-and-write).
- Keep the OSS core footprint minimal and documented (see §8) so upstream merges stay clean.

**Ask first (decision needed before implementing)**
- Anything in Open Questions §7 (data-scoping deferral, per-creator key visibility, default role for unmapped routes, `admin@localhost` email handling).
- Adding any new admin-only `/api/*` surface not in the §4.2 map.

**Never**
- Never re-hash the migrated `admin_password` (double-bcrypt breaks login).
- Never delete the legacy `governance_config` admin keys in this phase (rollback path).
- Never gate the inference path (`/v1/*`) by user role — it is virtual-key governed.
- Never add `DELETE /api/users/:id` (soft-delete only).
- Never return raw API key values to a `viewer`.
- Never expand scope into team membership, access profiles, SSO, SCIM, or audit logging — those are later phases.

---

## 7. Open Questions (resolve before / during planning)

1. **Data scoping deferral.** Confirm Phase 1 RBAC is role-only (no per-team/per-row filtering), with all scoping moved to Phase 2.4. The roadmap's Feature 1.2 text mixes the two; this spec assumes the split. **Recommended: yes, defer.**
2. **Per-creator key visibility.** "Operator sees only keys they created" needs a `created_by` column. Deferred to Phase 2.4 here. Confirm acceptable, or pull a minimal `created_by` into Phase 1.
3. **Default role for unmapped `/api/*` routes.** Resolved to **fail-closed** (unknown mutation → admin, unknown read → operator) after review found an unmapped `/api/config` would otherwise be operator-writable despite mutating auth/security. Trade-off: a newly added operator/viewer route returns `403` until explicitly mapped — acceptable, and the safe direction. Confirm you accept "map-or-denied" over "map-or-leaked".
4. **`admin@localhost` placeholder email.** Login with `admin@localhost` works post-migration; release notes must communicate it. Confirm we keep the placeholder vs. prompting the admin to set a real email on first login.

---

## 8. Implementation Overview (overlay architecture)

This is a **fork** of open-source Bifrost adding enterprise-style features, so the implementation minimizes edits to upstream core (to keep upstream merges clean). The full task-level detail is in the plan; the shape is:

**New, fork-owned package — `transports/bifrost-http/identity/`** (holds essentially all logic):
- user + session-association storage (own tables; does **not** alter core session storage)
- automatic, idempotent migration of the legacy single admin (run via the core-provided downstream-migration hook)
- authentication-resolution + authorization (RBAC) middleware, and a login interceptor that serves email login (so the legacy username login is never reached)
- `/api/users*` and `/api/auth/settings` handlers

**OSS core footprint — three documented "fork patches" only** (recorded in `FORK_PATCHES.md`):
1. **server bootstrap** — append the overlay's middlewares to the API chain + one `Wire(...)` call (≈2 lines).
2. **provider/key serialization** — mask raw key values from `viewer` (the one place core output must be transformed; §4.5).
3. **UI** — login form sends `email`; dashboard gates controls by role (§4.6).

**Explicitly NOT changed:** core session storage, the config-store interface, the core auth/session middleware, and the core login handler. The `IsEnterprise` flag (upstream's proprietary edition) is **not** used.

> See `docs/superpowers/plans/2026-05-30-phase1-identity-foundation-overlay.md` for the authoritative, task-by-task implementation.

---

## 9. Acceptance Criteria (Phase 1 done)

1. Upgrading a single-admin instance migrates silently; the existing admin is **not** logged out and logs in with the same password via their email.
2. An admin creates an `operator` and a `viewer`; both log in.
3. The `operator` receives `403` on `/api/users`; can manage providers/keys/governance/MCP/plugins.
4. The `viewer` receives `403` on every mutating call; can read resources; sees masked key values.
5. Role checks hold against raw HTTP, independent of UI state.
6. Deactivating a user blocks their next request (`401`) without an explicit logout; a role change applies on the next request.
7. The last admin cannot be deactivated or demoted, even under concurrent requests.
8. All Part A + Part B tests in §5 pass on both SQLite and Postgres.
