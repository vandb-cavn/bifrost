// Package handlers provides HTTP request handlers for the Bifrost HTTP transport.
package handlers

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// UserGovernanceSync is the subset of the governance plugin's in-memory store
// interface needed to keep inference-time quota tracking in sync with DB writes.
type UserGovernanceSync interface {
	CreateUserGovernanceInMemory(userID string, budget *tables.TableBudget, rateLimit *tables.TableRateLimit)
	UpdateUserGovernanceInMemory(userID string, budget *tables.TableBudget, rateLimit *tables.TableRateLimit)
	DeleteUserGovernanceInMemory(userID string)
}

// GovernanceUsersHandler manages HTTP requests for user governance CRUD.
type GovernanceUsersHandler struct {
	configStore    configstore.ConfigStore
	governanceSync UserGovernanceSync // nil when governance plugin not loaded
}

// NewGovernanceUsersHandler creates a new users handler instance.
func NewGovernanceUsersHandler(cs configstore.ConfigStore, govSync UserGovernanceSync) *GovernanceUsersHandler {
	return &GovernanceUsersHandler{configStore: cs, governanceSync: govSync}
}

// RegisterRoutes registers the user governance routes.
func (h *GovernanceUsersHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/users", lib.ChainMiddlewares(h.listUsers, middlewares...))
	r.POST("/api/governance/users", lib.ChainMiddlewares(h.createUser, middlewares...))
	r.GET("/api/governance/users/{id}", lib.ChainMiddlewares(h.getUser, middlewares...))
	r.PUT("/api/governance/users/{id}", lib.ChainMiddlewares(h.updateUser, middlewares...))
	r.DELETE("/api/governance/users/{id}", lib.ChainMiddlewares(h.deleteUser, middlewares...))
}

func isNotFound(err error) bool {
	return errors.Is(err, configstore.ErrNotFound)
}

func isUniqueConstraintError(err error) bool {
	return errors.Is(err, configstore.ErrAlreadyExists) ||
		strings.Contains(strings.ToLower(err.Error()), "unique") ||
		strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}

func (h *GovernanceUsersHandler) listUsers(ctx *fasthttp.RequestCtx) {
	search := string(ctx.QueryArgs().Peek("search"))
	limit := int(ctx.QueryArgs().GetUintOrZero("limit"))
	offset := int(ctx.QueryArgs().GetUintOrZero("offset"))
	if limit == 0 {
		limit = 50
	}

	users, total, err := h.configStore.ListUsers(ctx, search, limit, offset)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"users": users, "total_count": total})
}

func (h *GovernanceUsersHandler) createUser(ctx *fasthttp.RequestCtx) {
	var body struct {
		Email       string  `json:"email"`
		Name        string  `json:"name"`
		TeamID      *string `json:"team_id"`
		BudgetID    *string `json:"budget_id"`
		RateLimitID *string `json:"rate_limit_id"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}
	body.Email = strings.TrimSpace(body.Email)
	if body.Email == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "email is required")
		return
	}

	now := time.Now().UTC()
	user := &tables.TableUser{
		ID:          uuid.NewString(),
		Email:       body.Email,
		Name:        body.Name,
		TeamID:      body.TeamID,
		BudgetID:    body.BudgetID,
		RateLimitID: body.RateLimitID,
		AuthMethod:  "password",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.configStore.CreateUser(ctx, user); err != nil {
		if isUniqueConstraintError(err) {
			SendError(ctx, fasthttp.StatusConflict, "user with this email already exists")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.governanceSync != nil {
		h.governanceSync.CreateUserGovernanceInMemory(user.ID, nil, nil)
	}
	SendJSONWithStatus(ctx, map[string]any{"user": user}, fasthttp.StatusCreated)
}

func (h *GovernanceUsersHandler) getUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	user, err := h.configStore.GetUser(ctx, id)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "user not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"user": user})
}

func (h *GovernanceUsersHandler) updateUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var body map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}

	updates := map[string]any{
		"updated_at": time.Now().UTC(),
	}
	for _, key := range []string{"name", "team_id", "budget_id", "rate_limit_id"} {
		if value, ok := body[key]; ok {
			updates[key] = value
		}
	}

	user, err := h.configStore.UpdateUser(ctx, id, updates)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "user not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.governanceSync != nil {
		h.governanceSync.UpdateUserGovernanceInMemory(user.ID, nil, nil)
	}
	SendJSON(ctx, map[string]any{"user": user})
}

func (h *GovernanceUsersHandler) deleteUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteUser(ctx, id); err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "user not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.governanceSync != nil {
		h.governanceSync.DeleteUserGovernanceInMemory(id)
	}
	SendJSON(ctx, map[string]any{"message": "user deleted"})
}
