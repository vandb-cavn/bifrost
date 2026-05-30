# Fork Patches (carry across upstream merges)

This fork adds multi-user + RBAC as an overlay package `transports/bifrost-http/identity/`.
The only edits to upstream files are listed here. On `git merge upstream/main`, re-apply these.

## Patch #1 — wire the overlay (transports/bifrost-http/server/server.go)
- In bootstrap, inside `if IsEnterprise == nil`, append `identity.Middlewares(...)` to `apiMiddlewares`.
- After `RegisterAPIRoutes(...)`, call `identity.Wire(s.Ctx, s.Router, s.Config.ConfigStore)`.
- Add the `identity` import.

## Patch #2 — viewer key masking (transports/bifrost-http/handlers/providers.go, transports/bifrost-http/handlers/provider_keys.go)
- Hide raw key values when the request user is a viewer.

## Patch #3 — UI (ui/)
- Login form sends `email` instead of `username`; dashboard gates controls by role.
