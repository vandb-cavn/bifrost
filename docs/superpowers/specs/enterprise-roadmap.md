# Bifrost Enterprise Features — Analysis & Roadmap

**Last updated:** 2026-04-19 (rev 2 — SSO admin architecture)  
**Scope:** Enterprise features to implement in the OSS codebase (bifost2)

---

## 1. Current State Assessment

### ✅ Fully Implemented (OSS)

| Feature | DB | API | UI | Notes |
|---|---|---|---|---|
| Teams | ✅ | ✅ | ✅ | Full CRUD, budget + rate limit assignment |
| Customers | ✅ | ✅ | ✅ | Full CRUD, multi-tenancy |
| Virtual Keys | ✅ | ✅ | ✅ | Provider configs, MCP control, model allowlists |
| Budgets | ✅ | ✅ | ✅ | Calendar-aligned, flexible durations |
| Rate Limits | ✅ | ✅ | ✅ | Token + request limits |
| Routing Rules | ✅ | ✅ | ✅ | CEL expressions, weighted targets |
| Model Configs | ✅ | ✅ | ✅ | Per-model configuration |
| Pricing Overrides | ✅ | ✅ | ✅ | Scope-based overrides |
| Request Logging | ✅ | ✅ | ✅ | Inference logs, cost tracking |

### 🔴 Not Implemented (Enterprise Only — Stub/Fallback)

| Feature | DB | API | UI | Fallback Shows |
|---|---|---|---|---|
| User Management | ❌ | ❌ | Fallback | "Manage users, set per-user budgets and rate limits" |
| RBAC (Roles & Permissions) | ❌ | ❌ | Context only (always `true`) | "Unlock roles and permissions for better security" |
| Audit Logs | ❌ | ❌ | Fallback | "Unlock audit logs for better compliance" |
| SCIM Provisioning | ❌ | ❌ | Fallback | "Unlock SCIM based access management" |
| Access Profiles | ❌ | ❌ | Fallback | "Unlock access profiles for better performance" |
| Business Units | ❌ | ❌ | Fallback | "Unlock business units & advanced governance" |
| SSO / OIDC (Okta, Entra) | ❌ | ❌ | ❌ | Okta + Entra docs written, no backend |

### 🟡 Partial Infrastructure (OSS)

| Feature | Status |
|---|---|
| User governance tracking | Struct `UserGovernance` in `plugins/governance/store.go` — no DB persistence |
| RBAC resource/operation enums | Defined in `ui/app/_fallbacks/enterprise/lib/contexts/rbacContext.tsx` |
| RBAC context | Wired into UI but always returns `true` (no enforcement) |
| Sessions table | Exists (`framework/configstore/tables/sessions.go`) — no `user_id` field, single admin only |

---

## 2. Feature Specifications

### 2.1 User Management

**Goal:** Individual user accounts serving two distinct purposes on the same entity:
- **Admin UI users** — log into dashboard, controlled by RBAC roles
- **API consumer users** — call inference via virtual keys, controlled by budget/rate limits

**One `governance_users` table, three consumers:**
```
governance_users (id, email, name, role_id, budget_id, rate_limit_id, ...)
    │
    ├── SSO Handler (transport)     → auto-provision on IdP login
    ├── RBAC Middleware (transport) → user_id → role → permissions on /api/*
    └── Governance Plugin           → user_id → budget/rate limit on /v1/*
```

These three consumers are **independent** — governance plugin does NOT call user management at inference time. It reads its own in-memory store (keyed by user_id). User management CRUD is the only write path that updates both.

**Governance hierarchy when complete:**
```
Customer (org-level budget)
    └── Business Unit (division-level)
            └── Team (department-level budget)
                    └── User (individual-level budget + auth)
                            └── Virtual Key (API-level budget + rate limits)
```

**Core capabilities:**
- User creation (manual + SSO auto-provisioning)
- Per-user budget allocation
- Per-user rate limits
- User ↔ Team membership
- User ↔ Role assignment (RBAC dependency)
- Usage tracking per user

**API contracts (from docs):**
```
GET    /api/users
POST   /api/users
GET    /api/users/{user_id}
PUT    /api/users/{user_id}
DELETE /api/users/{user_id}
```

**DB tables needed:**
- `governance_users` — id, email, name, customer_id, budget_id, rate_limit_id, role_id, created_at, updated_at

---

### 2.2 RBAC — Roles & Permissions

**Goal:** Fine-grained access control for dashboard operations. Principle of least privilege.

**System roles (from docs):**
| Role | Permissions | Description |
|---|---|---|
| Admin | 42 | Full access to all resources and operations |
| Developer | 27 | CRUD on technical resources, view logs/cluster |
| Viewer | 14 | Read-only access to all resources |

**Protected resources (from `rbacContext.tsx` + docs):**
`Logs`, `ModelProvider`, `Observability`, `Plugins`, `VirtualKeys`, `UserProvisioning`, `Users`, `AuditLogs`, `GuardrailsConfig`, `GuardrailRules`, `Cluster`, `Settings`, `MCPGateway`, `AdaptiveRouter`

**Operations per resource:** `View`, `Create`, `Update`, `Delete`

**API contracts:**
```
GET    /api/roles
POST   /api/roles
GET    /api/roles/{role_id}
PUT    /api/roles/{role_id}
DELETE /api/roles/{role_id}
GET    /api/roles/{role_id}/permissions
PUT    /api/roles/{role_id}/permissions     { "permission_ids": [1,2,3] }
```

**DB tables needed:**
- `governance_roles` — id, name, description, is_system (bool), created_at
- `governance_permissions` — id, resource, operation
- `governance_role_permissions` — role_id, permission_id (join table)
- `governance_user_roles` — user_id, role_id (join table)

**Enforcement point:** New RBAC middleware in the **transport layer** — runs on all `/api/*` routes alongside existing `AuthMiddleware`. Resolves `session.user_id → roles → permissions → allow/deny`. The governance plugin is NOT involved in this enforcement path.

---

### 2.3 SSO / OIDC Authentication

**Goal:** Single Sign-On via Okta and Microsoft Entra ID for admin UI access. Users auto-provisioned on first login with roles synced from IdP.

**Architecture:** Lives entirely in the **transport layer** (new `SSOHandler` alongside existing `SessionHandler`). Does not touch the governance plugin.

**Supported providers:**
- Okta (OIDC + custom `bifrostRole` attribute + group mapping)
- Microsoft Entra ID (app roles + group claims)

**Flow:**
```
User hits /login
    → show SSO button (if sso_config enabled)
    → GET /api/sso/{provider}/start → redirect to IdP
    → IdP authenticates → callback to GET /api/sso/{provider}/callback
    → Bifrost validates JWT, extracts role claims
    → Auto-provision user in governance_users (if first login)
    → Assign highest-privilege role from claims
    → Create session with user_id → set cookie
    → redirect to /workspace
```

**Role mapping:**
- Roles take precedence over groups
- When user has multiple roles → assign highest privilege
- Claims configurable per IdP setup

**Impact on existing `SessionsTable`:**
- Add nullable `user_id` column — `NULL` = legacy single-admin login (backward compat), non-`NULL` = SSO user
- Existing password login continues to work unchanged

**DB tables needed:**
- `governance_sso_configs` — id, provider (okta/entra), client_id, client_secret (encrypted), issuer_url, role_claim_key, group_claim_key, enabled
- Migration: add `user_id TEXT NULL` to `sessions` table

**API contracts:**
```
GET  /api/sso/{provider}/start     → redirect to IdP
GET  /api/sso/{provider}/callback  → handle OIDC callback, create session
GET  /api/sso/config               → get SSO config (admin only)
PUT  /api/sso/config               → update SSO config (admin only)
```

**Dependency:** User Management must exist first (auto-provisioning creates users in `governance_users`).

---

### 2.4 SCIM — Identity Provider Sync

**Goal:** Automated user and group provisioning from identity providers. No manual user management.

**Supported providers (from enterprise changelog v1.4.0-prerelease2):**
- Okta
- Microsoft Entra ID
- Google Workspace
- Keycloak
- Zitadel
- SailPoint

**SCIM 2.0 protocol endpoints:**
```
GET    /scim/v2/Users
POST   /scim/v2/Users
GET    /scim/v2/Users/{id}
PUT    /scim/v2/Users/{id}
PATCH  /scim/v2/Users/{id}
DELETE /scim/v2/Users/{id}

GET    /scim/v2/Groups
POST   /scim/v2/Groups
GET    /scim/v2/Groups/{id}
PUT    /scim/v2/Groups/{id}
PATCH  /scim/v2/Groups/{id}
DELETE /scim/v2/Groups/{id}
```

**Capabilities:**
- Create/update/deprovision users from IdP
- Sync groups → Bifrost Teams
- Attribute mapping (IdP fields → Bifrost user fields)
- Role assignment via group membership

**DB tables needed:**
- `governance_scim_configs` — id, provider, bearer_token (encrypted), attribute_mappings (json), enabled
- `governance_scim_sync_log` — id, provider, event_type, status, details, synced_at

**Dependency:** User Management + SSO/OIDC must exist first.

---

### 2.5 Audit Logs

**Goal:** Immutable, compliance-ready audit trail for all security-relevant events.

**Event categories (from docs):**

| Category | Events |
|---|---|
| Authentication | Login success/fail, logout, session expiry, MFA, account lockout, SSO |
| Authorization | Model access, provider access, VK usage, budget checks, permission denials |
| Configuration | VK/team/user CRUD, budget changes, rate limit changes, provider key updates, guardrail changes |
| Data Access | PII detection, data export, log queries, sensitive config access |
| Security | Prompt injection attempts, jailbreak attempts, unusual access, rate limit violations, guardrail violations |

**API contracts:**
```
GET  /api/audit-logs                               query params: event_type, user_id, start_date, end_date, severity, status
POST /api/audit-logs/query                         advanced filtering
POST /api/audit-logs/reports                       generate compliance reports (SOC2, GDPR, ISO27001, HIPAA)
GET  /api/audit-logs/{event_id}
```

**Export integrations:** Splunk (HEC), Datadog, Elastic, Webhook

**DB tables needed:**
- `governance_audit_events` — id, event_id, timestamp, event_type, action, status, severity, actor_user_id, actor_ip, resource_type, resource_id, details (json), verification_hash

**Immutability:** Each event gets a cryptographic hash (SHA-256 over prev_hash + event content) → tamper-proof chain.

**Dependency:** User Management (actor tracking). RBAC controls who can read audit logs (`AuditLogs` resource).

---

### 2.6 Access Profiles

**Goal:** Reusable permission bundles controlling model/provider access. Applied to teams, business units, or users.

**Core concept:**
> Instead of configuring allowed models per virtual key, create a profile ("GPT-4 Only", "Internal Models", "Cost-Controlled") and attach it to a team or user.

**Capabilities (from enterprise changelog):**
- Define model access rules at profile level
- Propagate profiles down the hierarchy (Business Unit → Teams → Users)
- Propagation dialogs for bulk assignment
- Full CRUD UI

**DB tables needed:**
- `governance_access_profiles` — id, name, description, config (json: allowed_models, allowed_providers, max_cost_per_request), created_at
- `governance_access_profile_assignments` — id, profile_id, entity_type (team/business_unit/user), entity_id

**Dependency:** RBAC (who can manage profiles), User Management (assign to users).

---

### 2.7 Business Units

**Goal:** Add organizational hierarchy layer between Customer and Teams.

**Current hierarchy:** `Customer → Teams → Virtual Keys`  
**Target hierarchy:** `Customer → Business Units → Teams → Users → Virtual Keys`

**Capabilities:**
- Budget allocation at business unit level
- Team grouping within business units
- User sync dialogs (from enterprise changelog)
- Inherit access profiles from business unit to teams

**API contracts:**
```
GET    /api/governance/business-units
POST   /api/governance/business-units
GET    /api/governance/business-units/{id}
PUT    /api/governance/business-units/{id}
DELETE /api/governance/business-units/{id}
GET    /api/governance/business-units/{id}/teams
```

**DB tables needed:**
- `governance_business_units` — id, name, customer_id, budget_id, rate_limit_id, created_at
- Add `business_unit_id` FK to `governance_teams`

**Dependency:** User Management (users in business units), Access Profiles (profile propagation).

---

## 3. Dependency Graph

```
SSO / OIDC ──────────────────────────────────────────────┐
                                                          ↓
User Management ──→ RBAC ──→ Audit Logs                  │
       │                │         │                       │
       │                └──→ Access Profiles              │
       │                          │                       │
       └──→ Business Units ───────┘                       │
                   │                                      │
                   └──→ SCIM ←─────────────────────────────┘
```

### Hard dependencies (cannot build B without A):

| Feature B | Requires A |
|---|---|
| RBAC | User Management (assign roles to users) |
| SSO / OIDC | User Management (auto-provisioning on login) |
| SCIM | User Management + SSO/OIDC (provision into existing user system) |
| Audit Logs | User Management (actor_user_id tracking) |
| Access Profiles | RBAC (who can manage profiles) |
| Business Units | User Management (users live in BUs) |

### Soft dependencies (can build partially without, but degraded):

| Feature B | Soft Dependency | Impact if Missing |
|---|---|---|
| Audit Logs | RBAC | Cannot restrict who reads audit logs |
| Access Profiles | Business Units | Cannot propagate profiles down BU hierarchy |
| SCIM | RBAC | Cannot map IdP groups to Bifrost roles automatically |

---

## 4. Implementation Roadmap

### Phase 1 — Identity Foundation
**Goal:** Establish user identity, SSO admin access, and RBAC enforcement. Everything else builds on this.

| # | Task | Layer | Effort | Output |
|---|---|---|---|---|
| 1.1 | `governance_users` table + CRUD API | DB + GovernanceHandler | M | User entity, manual create/update/delete |
| 1.2 | `governance_roles/permissions` tables + seed system roles | DB | S | Admin/Developer/Viewer roles pre-seeded |
| 1.3 | RBAC middleware | Transport | M | `/api/*` routes enforce permissions via `session.user_id → role` |
| 1.4 | `governance_sso_configs` table + `user_id` on `sessions` | DB migration | S | Backward-compat session migration |
| 1.5 | `SSOHandler` — Okta OIDC flow | Transport | M | Login via Okta, auto-provision user, session with `user_id` |
| 1.6 | `SSOHandler` — Microsoft Entra flow | Transport | M | Login via Entra ID |
| 1.7 | SSO config UI + User Management UI | Frontend | M | Admin can configure IdP, view/manage users |

**Implementation order within phase:** 1.1 → 1.2 → 1.3 (can run parallel) → 1.4 → 1.5 → 1.6 → 1.7

**Exit criteria:** Users can log in via SSO (Okta/Entra), are auto-provisioned into `governance_users`, assigned RBAC roles, and `/api/*` routes enforce those roles. Password login still works unchanged.

---

### Phase 2 — Compliance & Audit
**Goal:** Enterprise compliance requirements. Can be demoed to customers for SOC2/GDPR readiness.

| # | Feature | Effort | Output |
|---|---|---|---|
| 2.1 | Audit Logs | M | `governance_audit_events` table, query API, UI, export |
| 2.2 | Audit Immutability | S | Cryptographic hash chain on audit events |
| 2.3 | SIEM Integration | M | Splunk / Datadog / Elastic export connectors |

**Exit criteria:** All auth, config change, and security events are logged. Admins can query, filter, and export audit trails.

---

### Phase 3 — Organizational Hierarchy
**Goal:** Enterprise org structure with fine-grained access control.

| # | Feature | Effort | Output |
|---|---|---|---|
| 3.1 | Business Units | M | `governance_business_units` table, CRUD API, team assignment |
| 3.2 | Access Profiles | M | Profile definition, hierarchy propagation, CRUD UI |

**Exit criteria:** Organizations can model their department structure in Bifrost and apply access policies at each level.

---

### Phase 4 — Automated Provisioning
**Goal:** Zero-touch user and group management via identity providers.

| # | Feature | Effort | Output |
|---|---|---|---|
| 4.1 | SCIM Core (Okta) | L | SCIM 2.0 endpoints, user/group sync |
| 4.2 | SCIM Expansion | L | Google Workspace, Keycloak, Zitadel, SailPoint |
| 4.3 | Group → Team sync | M | IdP groups automatically create/update Bifrost teams |

**Exit criteria:** Adding a user to an Okta group automatically provisions them in Bifrost with correct role and team membership.

---

### Phase 5 — Future Enterprise Features
> To be specced as requirements emerge.

| Feature | Description |
|---|---|
| Compliance Dashboards | SOC2/GDPR/ISO27001/HIPAA status dashboards |
| Automated Compliance Monitoring | CEL-based rules triggering alerts on policy violations |
| Data Residency Controls | Restrict which providers can process data by region |
| MFA Enforcement | Require MFA for specific roles or operations |
| Break-glass Access | Emergency elevated access with mandatory audit log |
| API Key Rotation Policies | Automated rotation schedules for provider keys |
| User Activity Rankings | Dashboard tracking per-user model usage |
| Adaptive Load Balancing UI | UI for the existing adaptive load balancing plugin |

---

## 5. Effort Estimates

| Label | Definition |
|---|---|
| S (Small) | 1-2 days — DB migration + API endpoint + UI wiring |
| M (Medium) | 3-5 days — New DB schema + multiple API endpoints + UI views |
| L (Large) | 1-2 weeks — Protocol implementation, complex flows, multiple integrations |

| Phase | Features | Total Effort |
|---|---|---|
| Phase 1 | User Mgmt + SSO + RBAC | ~3 weeks |
| Phase 2 | Audit Logs + SIEM | ~2 weeks |
| Phase 3 | Business Units + Access Profiles | ~2 weeks |
| Phase 4 | SCIM (Okta + 4 more) | ~3 weeks |
| **Total** | | **~10 weeks** |

---

## 6. Technical Notes

### Architecture separation: Transport vs Plugin

Two distinct layers handle user-related concerns — they share the `governance_users` table but never call each other at request time:

```
governance_users (shared DB table)
        │
        ├── Transport layer (SSOHandler, RBAC middleware, SessionHandler)
        │       Handles: admin UI login, session management, /api/* access control
        │       Does NOT use governance plugin hooks
        │
        └── Governance plugin (inference pipeline)
                Handles: budget/rate limit checks on /v1/* inference requests
                Reads: in-memory UserGovernance store (user_id → budget + rate limit)
                Does NOT query governance_users at inference time
```

Write path: User Management CRUD → updates `governance_users` AND calls `governance.UpdateUserGovernanceInMemory()`.

### DB migration strategy
- All new tables prefixed `governance_` consistent with existing schema
- Additive-only migrations (no breaking changes to existing tables)
- `sessions` table: add nullable `user_id TEXT` — NULL = legacy single-admin, non-NULL = SSO user
- `governance_teams` gets new nullable FK `business_unit_id` (non-breaking)

### SSO handler
- New `SSOHandler` in `transports/bifrost-http/handlers/sso.go` alongside existing `session.go`
- Implements OIDC Authorization Code flow (no implicit flow)
- State param (nonce) stored in short-lived DB record to prevent CSRF
- On callback: validate JWT → upsert `governance_users` → create `sessions` with `user_id` → set cookie

### RBAC enforcement
- New `RBACMiddleware` in transport layer — runs after existing `AuthMiddleware`
- `AuthMiddleware` validates token exists and is not expired (unchanged)
- `RBACMiddleware` resolves `session.user_id → role → permissions → allow/deny`
- Permission check: `canUser(userID, resource, operation) bool`
- Cache resolved permissions per user (in-memory, TTL 60s)
- If `session.user_id` is NULL (legacy single-admin) → allow all (backward compat)
- OSS without enterprise flag: RBAC middleware no-ops (preserve current behavior)

### Audit log write path
- Fire-and-forget: write to `governance_audit_events` asynchronously
- Never block the request path for audit writes
- Use buffered channel → background goroutine flush (batch size 100, flush every 5s)
- Hash chain: `SHA256(prev_hash || event_id || timestamp || actor || action || resource || details)`

### Enterprise feature flag
- Current pattern: OSS shows `_fallbacks/enterprise/` stub components
- New pattern: `isEnterprise bool` in governance plugin config gates backend features
- Frontend: keep existing fallback pattern — no changes needed to feature flag system

### API versioning
- All new endpoints under `/api/` — consistent with existing governance endpoints
- No `/api/enterprise/` prefix needed — enterprise is the same server with features enabled
