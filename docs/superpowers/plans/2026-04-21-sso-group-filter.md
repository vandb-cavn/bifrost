# Implementation Plan: SSO Group Filter

**Date:** 2026-04-21  
**Branch:** feat/phase0-phase1-users-sso

---

## Overview

Extend SSO login with group-based access control:

**Group Filter** — Only users who belong to at least one of the configured `allowed_groups` can log in. If `allowed_groups` is empty, all authenticated users are allowed (current behavior preserved).

Builds on the existing `GroupClaimKey` infrastructure — all SSO adapters already extract `groups []string` from claims. The callback currently discards them (`email, name, _, err`). This plan wires them through for access control only.

> **Group → Team Sync is deferred to Phase 4 (SCIM).** The SCIM phase includes full bidirectional group/team sync (Phase 4.3).

---

## Architecture Decisions

- **JSON column, not new table**: `allowed_groups` stored as TEXT in `governance_sso_configs`.
- **Additive migration**: New column is NOT NULL with empty default — all existing SSO configs continue to work without change.
- **Group filter enforced at callback**: Not at token verification time. Simpler, consistent with how role claims are processed.
- **Fail-closed on parse error**: If `allowed_groups` is set but JSON is malformed, deny login rather than silently allowing all.
- **Normalize group names**: Both stored groups and IdP-returned groups are lowercased + trimmed before comparison. This avoids case/whitespace mismatches across IdPs.

---

## Current State

| Layer | Status |
|-------|--------|
| `GroupClaimKey` config field | ✅ exists |
| Groups extracted in all adapters | ✅ all `ExtractUserInfo` return `groups []string` |
| Groups used in callback | ❌ discarded with `_` |
| `AllowedGroups` in SSO config | ❌ missing |
| Frontend fields | ❌ missing |

---

## Task List

---

### Task 1: DB Migration — add `allowed_groups` column

**Description:** Add one new TEXT column to `governance_sso_configs`. Defaults to empty string (= no filter). Wire the migration into `triggerMigrations`.

**Acceptance criteria:**
- [ ] `governance_sso_configs` gains `allowed_groups TEXT NOT NULL DEFAULT ''`
- [ ] Migration runs cleanly on existing DB (idempotent via migration ID)
- [ ] Existing SSO configs unaffected (empty string = no filter)

**Files:**
- `framework/configstore/tables/sso.go` — add field + helpers to `TableGovernanceSSOConfig`
- `framework/configstore/migrations.go` — new `migrationAddSSOGroupFilter` function + call in `triggerMigrations`

**Implementation:**

In `tables/sso.go`, add to `TableGovernanceSSOConfig`:
```go
AllowedGroups string `gorm:"type:text;not null;default:''" json:"-"`
```

Add helpers on the struct. **Key invariants:**
- `GetAllowedGroups` returns `([]string, error)` — caller must handle error as deny
- `SetAllowedGroups` sanitizes input: trims whitespace, removes empty strings, deduplicates, lowercases

```go
// GetAllowedGroups returns the parsed allowed groups list.
// Returns an error if AllowedGroups is non-empty but malformed — callers must
// treat this as a deny (fail-closed) to prevent bypassing the filter.
func (c *TableGovernanceSSOConfig) GetAllowedGroups() ([]string, error) {
    if c.AllowedGroups == "" {
        return nil, nil
    }
    var groups []string
    if err := json.Unmarshal([]byte(c.AllowedGroups), &groups); err != nil {
        return nil, fmt.Errorf("allowed_groups is malformed: %w", err)
    }
    return groups, nil
}

func (c *TableGovernanceSSOConfig) SetAllowedGroups(groups []string) {
    seen := make(map[string]bool)
    sanitized := make([]string, 0, len(groups))
    for _, g := range groups {
        g = strings.ToLower(strings.TrimSpace(g))
        if g == "" || seen[g] {
            continue
        }
        seen[g] = true
        sanitized = append(sanitized, g)
    }
    if len(sanitized) == 0 {
        c.AllowedGroups = ""
        return
    }
    b, _ := json.Marshal(sanitized)
    c.AllowedGroups = string(b)
}
```

New migration:
```go
func migrationAddSSOGroupFilter(ctx context.Context, db *gorm.DB) error {
    m := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
        ID: "add_sso_group_filter_v1",
        Migrate: func(tx *gorm.DB) error {
            tx = tx.WithContext(ctx)
            if !tx.Migrator().HasColumn(&tables.TableGovernanceSSOConfig{}, "allowed_groups") {
                if err := tx.Migrator().AddColumn(&tables.TableGovernanceSSOConfig{}, "allowed_groups"); err != nil {
                    return fmt.Errorf("add allowed_groups: %w", err)
                }
            }
            return nil
        },
        Rollback: func(tx *gorm.DB) error { return nil },
    }})
    if err := m.Migrate(); err != nil {
        return fmt.Errorf("error running add_sso_group_filter_v1: %w", err)
    }
    return nil
}
```

**Verification:**
- [ ] `go build ./framework/configstore/...` passes
- [ ] Restart server → no migration errors in logs

**Estimated scope:** Small (2 files)

---

### Task 2: Backend — Wire group filter in SSO callback

**Description:** In `sso.go` callback handler, use the groups returned by `ExtractUserInfo` (currently discarded). Deny login if `allowed_groups` is configured and user is not in any of them.

**Acceptance criteria:**
- [ ] User not in `allowed_groups` gets 401 "not authorized for this application"
- [ ] User in `allowed_groups` (or `allowed_groups` empty) logs in normally
- [ ] If `allowed_groups` JSON is malformed in DB, login is denied (fail-closed), not silently allowed
- [ ] Group comparison is case-insensitive and whitespace-trimmed
- [ ] Existing behavior (no groups configured) is identical to before

**Files:**
- `transports/bifrost-http/handlers/sso.go` — update callback to use groups

**Implementation:**

In `sso.go` callback, replace:
```go
email, name, _, err := provider.ExtractUserInfo(claims, cfg)
```
with:
```go
email, name, groups, err := provider.ExtractUserInfo(claims, cfg)
```

After the email check, add:
```go
// Group filter: deny login if allowed_groups configured and user not in any.
// Fail-closed: if allowed_groups is set but malformed, deny login.
allowedGroups, err := cfg.GetAllowedGroups()
if err != nil {
    h.logger.Error("sso: allowed_groups malformed, denying login", zap.Error(err))
    SendError(ctx, fasthttp.StatusUnauthorized, "not authorized for this application")
    return
}
if len(allowedGroups) > 0 {
    allowedSet := make(map[string]bool, len(allowedGroups))
    for _, g := range allowedGroups {
        allowedSet[g] = true // already lowercased by SetAllowedGroups
    }
    allowed := false
    for _, g := range groups {
        if allowedSet[strings.ToLower(strings.TrimSpace(g))] {
            allowed = true
            break
        }
    }
    if !allowed {
        SendError(ctx, fasthttp.StatusUnauthorized, "not authorized for this application")
        return
    }
}
```

**Verification:**
- [ ] `go build ./transports/bifrost-http/...` passes
- [ ] Login with user NOT in `allowed_groups` → 401 response
- [ ] Login with user IN `allowed_groups` → redirects to /workspace
- [ ] Corrupt `allowed_groups` JSON in DB → login denied, error logged

**Estimated scope:** Small (1 file)

**Dependencies:** Task 1

---

### Task 3: SSO Config API — expose `allowed_groups` field

**Description:** Update the SSO config create/update/get API handlers to accept and return `allowed_groups`. Sanitization is handled by `SetAllowedGroups` — API handler passes through directly.

**Acceptance criteria:**
- [ ] `POST /api/governance/sso/configs` accepts `allowed_groups: string[]`
- [ ] `PUT /api/governance/sso/configs/:id` accepts same field
- [ ] `GET /api/governance/sso/configs` returns `allowed_groups` in response (normalized: lowercase, trimmed, no duplicates)
- [ ] Empty array or omitted field stores as empty string in DB

**Files:**
- `transports/bifrost-http/handlers/sso.go` — update `createConfig`, `updateConfig`, `safeConfigResponse`

**Implementation:**

In the create/update body struct, add:
```go
AllowedGroups []string `json:"allowed_groups"`
```

In `createConfig`/`updateConfig`, before saving:
```go
cfg.SetAllowedGroups(body.AllowedGroups) // sanitizes: lowercase, trim, dedupe
```

In `safeConfigResponse`, add:
```go
"allowed_groups": cfg.GetAllowedGroups(), // returns []string or nil
```

Note: `safeConfigResponse` now calls `GetAllowedGroups()` which returns `([]string, error)`. Since this is a read path (not login), log the error and return `nil` (empty):
```go
allowedGroups, err := cfg.GetAllowedGroups()
if err != nil {
    h.logger.Error("sso: allowed_groups malformed in safeConfigResponse", zap.Error(err))
    allowedGroups = nil
}
// ...
"allowed_groups": allowedGroups,
```

**Verification:**
- [ ] POST with `["Admin", " DevOps ", "admin"]` → GET returns `["admin", "devops"]` (normalized, deduped)
- [ ] POST with `allowed_groups: []` → GET returns `[]` or `null`

**Estimated scope:** Small (1 file)

**Dependencies:** Task 1

---

### Checkpoint: After Tasks 1–3

- [ ] `go build ./...` passes
- [ ] SSO login with `allowed_groups` configured: denied for non-members, allowed for members
- [ ] Case-insensitive: IdP group "ADMIN" matches configured "admin"
- [ ] Malformed JSON in DB → login denied, error in logs
- [ ] Existing SSO configs without group config work identically to before

---

### Task 4: Frontend types — add `allowed_groups` to SSOConfig

**Description:** Extend TypeScript types for SSO config to include `allowed_groups`.

**Acceptance criteria:**
- [ ] `SSOConfig` has `allowed_groups: string[]`
- [ ] `CreateSSOConfigRequest` and `UpdateSSOConfigRequest` have `allowed_groups?: string[]`

**Files:**
- `ui/lib/types/governance.ts`

```ts
export interface SSOConfig {
    // ...existing fields...
    allowed_groups: string[];
}

export interface CreateSSOConfigRequest {
    // ...existing fields...
    allowed_groups?: string[];
}

export interface UpdateSSOConfigRequest {
    // ...existing fields...
    allowed_groups?: string[];
}
```

**Verification:**
- [ ] `npm run build --prefix ui` passes with no TypeScript errors

**Estimated scope:** XS (1 file)

**Dependencies:** None (can be done in parallel with Tasks 1–3)

---

### Task 5: Frontend UI — `allowed_groups` in SSO config form

**Description:** Add an Allowed Groups section to the SSO config create/edit form — tag-style input where admin types group names and presses Enter to add. Shows current groups as removable badges.

**Acceptance criteria:**
- [ ] Allowed Groups section: add/remove group name tags, saved as `allowed_groups` array
- [ ] If `allowed_groups` is empty, hint "All users allowed" is shown
- [ ] Duplicate group names are rejected client-side (case-insensitive check)
- [ ] Whitespace is trimmed before adding a tag
- [ ] Saved values persist and reload correctly when editing existing config
- [ ] Form submits `allowed_groups` in create and update requests
- [ ] Warning shown: "Ensure Group Claim Key is correctly configured before enabling group filter"

**Files:**
- `ui/app/workspace/scim/views/ssoConfigTab.tsx` (or equivalent SSO form component)

**UI sketch:**
```tsx
// State
const [allowedGroups, setAllowedGroups] = useState<string[]>(config?.allowed_groups ?? []);
const [groupInput, setGroupInput] = useState("");

function addGroup() {
    const trimmed = groupInput.trim().toLowerCase();
    if (!trimmed) return;
    // Prevent duplicates (case-insensitive — backend also normalizes, but guard in UI too)
    if (allowedGroups.includes(trimmed)) return;
    setAllowedGroups([...allowedGroups, trimmed]);
    setGroupInput("");
}

function removeGroup(g: string) {
    setAllowedGroups(allowedGroups.filter((x) => x !== g));
}

// Render
<div className="space-y-2">
    <Label>Allowed Groups</Label>
    <p className="text-muted-foreground text-xs">
        Only users in these IdP groups can log in. Leave empty to allow all.
    </p>
    {allowedGroups.length > 0 && (
        <p className="text-amber-600 text-xs">
            ⚠ Ensure the Group Claim Key is correctly configured in your IdP before enabling group filter.
        </p>
    )}
    <div className="flex flex-wrap gap-2">
        {allowedGroups.length === 0 && (
            <span className="text-muted-foreground text-xs">All users allowed</span>
        )}
        {allowedGroups.map((g) => (
            <Badge key={g} variant="secondary">
                {g}
                <button type="button" onClick={() => removeGroup(g)}>
                    <X className="h-3 w-3 ml-1" />
                </button>
            </Badge>
        ))}
    </div>
    <div className="flex gap-2">
        <Input
            placeholder="Enter group name and press Enter"
            value={groupInput}
            onChange={(e) => setGroupInput(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addGroup(); } }}
        />
        <Button type="button" variant="outline" onClick={addGroup}>Add</Button>
    </div>
</div>
```

**Verification:**
- [ ] Add "Admin" → stored/displayed as "admin" (lowercased)
- [ ] Add "admin" again → rejected silently (no duplicate)
- [ ] Add " devops " → stored as "devops" (trimmed)
- [ ] Remove a group → save → correctly removed
- [ ] Empty groups → "All users allowed" hint shown
- [ ] Warning appears when at least one group is configured
- [ ] UI build passes

**Estimated scope:** Small (1 file)

**Dependencies:** Tasks 3, 4

---

### Checkpoint: Final

- [ ] Full build passes (`go build ./...` + `npm run build --prefix ui`)
- [ ] End-to-end: configure `allowed_groups` → SSO login → group filter enforced
- [ ] Case normalization: IdP "ADMIN" matches configured "admin"
- [ ] Fail-closed: corrupt DB value → login denied
- [ ] Existing SSO configs without group config work identically to before

---

## Implementation Order

```
Task 1 (DB migration)
    │
    ├── Task 2 (backend callback)
    ├── Task 3 (API fields)
    └── Task 4 (TS types) ← can run in parallel with 1-3
            │
            └── Task 5 (UI)
```

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Malformed JSON in `allowed_groups` | High | `GetAllowedGroups` returns error; callback denies login (fail-closed) |
| IdP sends groups with different casing ("Admin" vs "admin") | High | Normalize to lowercase at save (backend) and at comparison (callback) |
| GroupClaimKey misconfigured → all users locked out | Medium | UI warning when groups configured; local admin auth remains available as fallback |
| Whitespace in group names from IdP or UI | Low | `TrimSpace` at save (SetAllowedGroups) and at comparison (callback) |

---

## Out of Scope

- **Group → Team Sync**: Deferred to Phase 4.3 (SCIM).
- **Session revocation on group change**: Enforced on next login only.
- **Bidirectional case sync**: Backend stores lowercase; UI displays as stored. IdP normalization is documented, not enforced server-side against IdP.
