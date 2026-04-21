package handlers

import (
	"encoding/json"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// GovernanceRBACHandler handles roles and permissions CRUD + my-permissions endpoint.
type GovernanceRBACHandler struct {
	configStore    configstore.ConfigStore
	rbacMiddleware *RBACMiddleware
}

// NewGovernanceRBACHandler creates a new RBAC handler.
func NewGovernanceRBACHandler(cs configstore.ConfigStore, rbac *RBACMiddleware) *GovernanceRBACHandler {
	return &GovernanceRBACHandler{configStore: cs, rbacMiddleware: rbac}
}

// RegisterRoutes registers RBAC-related routes.
func (h *GovernanceRBACHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	// Current user's permissions (used by frontend RbacContext)
	r.GET("/api/governance/rbac/my-permissions", lib.ChainMiddlewares(h.myPermissions, middlewares...))

	// Permissions catalogue (read-only — all authenticated users can see what exists)
	r.GET("/api/governance/rbac/permissions", lib.ChainMiddlewares(h.listPermissions, middlewares...))

	// Roles CRUD — gated on Users+Update (admin operation)
	requireManage := h.rbacMiddleware.RequirePermission("Users", "Update")
	r.GET("/api/governance/rbac/roles", lib.ChainMiddlewares(h.listRoles, append(middlewares, requireManage)...))
	r.POST("/api/governance/rbac/roles", lib.ChainMiddlewares(h.createRole, append(middlewares, requireManage)...))
	r.GET("/api/governance/rbac/roles/{role_id}", lib.ChainMiddlewares(h.getRole, append(middlewares, requireManage)...))
	r.PUT("/api/governance/rbac/roles/{role_id}", lib.ChainMiddlewares(h.updateRole, append(middlewares, requireManage)...))
	r.DELETE("/api/governance/rbac/roles/{role_id}", lib.ChainMiddlewares(h.deleteRole, append(middlewares, requireManage)...))
	r.PUT("/api/governance/rbac/roles/{role_id}/permissions", lib.ChainMiddlewares(h.setRolePermissions, append(middlewares, requireManage)...))

	// User-role assignment
	r.POST("/api/governance/rbac/users/{user_id}/roles", lib.ChainMiddlewares(h.assignUserRole, append(middlewares, requireManage)...))
	r.DELETE("/api/governance/rbac/users/{user_id}/roles/{role_id}", lib.ChainMiddlewares(h.removeUserRole, append(middlewares, requireManage)...))
	r.GET("/api/governance/rbac/users/{user_id}/roles", lib.ChainMiddlewares(h.getUserRoles, append(middlewares, requireManage)...))
}

func (h *GovernanceRBACHandler) myPermissions(ctx *fasthttp.RequestCtx) {
	token, _ := ctx.UserValue(schemas.BifrostContextKeySessionToken).(string)
	if token == "" {
		// Legacy admin: return all-allow signal
		SendJSON(ctx, map[string]any{"is_admin": true, "permissions": []any{}})
		return
	}
	session, err := h.configStore.GetSession(ctx, token)
	if err != nil || session == nil || session.UserID == nil {
		SendJSON(ctx, map[string]any{"is_admin": true, "permissions": []any{}})
		return
	}
	perms, err := h.configStore.GetUserPermissions(ctx, *session.UserID)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"is_admin": false, "permissions": perms})
}

func (h *GovernanceRBACHandler) listPermissions(ctx *fasthttp.RequestCtx) {
	perms, err := h.configStore.ListPermissions(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"permissions": perms})
}

func (h *GovernanceRBACHandler) listRoles(ctx *fasthttp.RequestCtx) {
	roles, err := h.configStore.ListRoles(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"roles": roles})
}

func (h *GovernanceRBACHandler) createRole(ctx *fasthttp.RequestCtx) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil || body.Name == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "name is required")
		return
	}
	now := time.Now().UTC()
	role := &tables.TableRole{
		ID:          uuid.NewString(),
		Name:        body.Name,
		Description: body.Description,
		IsSystem:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.configStore.CreateRole(ctx, role); err != nil {
		if isUniqueConstraintError(err) {
			SendError(ctx, fasthttp.StatusConflict, "role with this name already exists")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSONWithStatus(ctx, map[string]any{"role": role}, fasthttp.StatusCreated)
}

func (h *GovernanceRBACHandler) getRole(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("role_id").(string)
	role, err := h.configStore.GetRole(ctx, id)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "role not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	perms, err := h.configStore.GetRolePermissions(ctx, id)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"role": role, "permissions": perms})
}

func (h *GovernanceRBACHandler) updateRole(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("role_id").(string)
	var body map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}
	role, err := h.configStore.UpdateRole(ctx, id, body)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "role not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.rbacMiddleware != nil {
		h.rbacMiddleware.InvalidateAll()
	}
	SendJSON(ctx, map[string]any{"role": role})
}

func (h *GovernanceRBACHandler) deleteRole(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("role_id").(string)
	if err := h.configStore.DeleteRole(ctx, id); err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "role not found")
			return
		}
		SendError(ctx, fasthttp.StatusForbidden, err.Error())
		return
	}
	if h.rbacMiddleware != nil {
		h.rbacMiddleware.InvalidateAll()
	}
	SendJSON(ctx, map[string]any{"message": "role deleted"})
}

func (h *GovernanceRBACHandler) setRolePermissions(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("role_id").(string)
	var body struct {
		PermissionIDs []string `json:"permission_ids"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.configStore.SetRolePermissions(ctx, id, body.PermissionIDs); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.rbacMiddleware != nil {
		h.rbacMiddleware.InvalidateAll()
	}
	perms, err := h.configStore.GetRolePermissions(ctx, id)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"permissions": perms})
}

func (h *GovernanceRBACHandler) assignUserRole(ctx *fasthttp.RequestCtx) {
	userID := ctx.UserValue("user_id").(string)
	var body struct {
		RoleID string `json:"role_id"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil || body.RoleID == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "role_id is required")
		return
	}
	if err := h.configStore.AssignUserRole(ctx, userID, body.RoleID); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.rbacMiddleware != nil {
		h.rbacMiddleware.InvalidateUser(userID)
	}
	SendJSON(ctx, map[string]any{"message": "role assigned"})
}

func (h *GovernanceRBACHandler) removeUserRole(ctx *fasthttp.RequestCtx) {
	userID := ctx.UserValue("user_id").(string)
	roleID := ctx.UserValue("role_id").(string)
	if err := h.configStore.RemoveUserRole(ctx, userID, roleID); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.rbacMiddleware != nil {
		h.rbacMiddleware.InvalidateUser(userID)
	}
	SendJSON(ctx, map[string]any{"message": "role removed"})
}

func (h *GovernanceRBACHandler) getUserRoles(ctx *fasthttp.RequestCtx) {
	userID := ctx.UserValue("user_id").(string)
	roles, err := h.configStore.GetUserRoles(ctx, userID)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"roles": roles})
}
