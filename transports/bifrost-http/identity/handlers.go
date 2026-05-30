package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (o *Overlay) listUsers(ctx *fasthttp.RequestCtx) {
	users, err := o.store.ListUsers(context.Background())
	if err != nil {
		sendErr(ctx, 500, "failed to list users")
		return
	}
	sendJSON(ctx, 200, map[string]any{"users": users})
}

func (o *Overlay) me(ctx *fasthttp.RequestCtx) {
	if u := userFrom(ctx); u != nil {
		sendJSON(ctx, 200, u)
		return
	}
	sendErr(ctx, 401, "no authenticated user")
}

func (o *Overlay) createUser(ctx *fasthttp.RequestCtx) {
	var p struct{ Email, Name, Role, Password string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	email := strings.ToLower(strings.TrimSpace(p.Email))
	if email == "" || !strings.Contains(email, "@") {
		sendErr(ctx, 400, "invalid email")
		return
	}
	if !ValidRoles[p.Role] {
		sendErr(ctx, 400, "role must be one of: admin, operator, viewer")
		return
	}
	if len(p.Password) < 8 {
		sendErr(ctx, 400, "password must be at least 8 characters")
		return
	}
	if ex, _ := o.store.GetUserByEmail(context.Background(), email); ex != nil {
		sendErr(ctx, 409, "email already in use")
		return
	}
	hash, err := hashPassword(p.Password)
	if err != nil {
		sendErr(ctx, 500, "failed to hash password")
		return
	}
	now := time.Now()
	u := &IdentityUser{ID: uuid.New().String(), Email: email, Name: p.Name, Role: p.Role, PasswordHash: &hash, IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := o.store.CreateUser(context.Background(), u); err != nil {
		sendErr(ctx, 500, "failed to create user")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) getUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if !o.selfOrAdmin(ctx, id) {
		sendErr(ctx, 403, "forbidden: admin or self only")
		return
	}
	u, err := o.store.GetUserByID(context.Background(), id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) updateUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if !o.selfOrAdmin(ctx, id) {
		sendErr(ctx, 403, "forbidden: admin or self only")
		return
	}
	u, err := o.store.GetUserByID(context.Background(), id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	var p struct{ Name, Email string } // role/is_active are NOT mutable here
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	if p.Name != "" {
		u.Name = p.Name
	}
	if p.Email != "" {
		email := strings.ToLower(strings.TrimSpace(p.Email))
		if other, _ := o.store.GetUserByEmail(context.Background(), email); other != nil && other.ID != u.ID {
			sendErr(ctx, 409, "email already in use")
			return
		}
		u.Email = email
	}
	if err := o.store.UpdateUser(context.Background(), u); err != nil {
		sendErr(ctx, 500, "failed to update user")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) setRole(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if c := userFrom(ctx); c != nil && c.ID == id {
		sendErr(ctx, 400, "cannot change your own role")
		return
	}
	var p struct{ Role string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil || !ValidRoles[p.Role] {
		sendErr(ctx, 400, "role must be one of: admin, operator, viewer")
		return
	}

	var u IdentityUser
	err := o.store.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if u.Role == RoleAdmin && p.Role != RoleAdmin {
			var count int64
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&IdentityUser{}).
				Where("role = ? AND is_active = ?", RoleAdmin, true).Count(&count).Error; err != nil {
				return err
			}
			if count <= 1 {
				return errors.New("cannot remove the last admin")
			}
		}
		u.Role = p.Role
		u.UpdatedAt = time.Now()
		return tx.Save(&u).Error
	})

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendErr(ctx, 404, "user not found")
			return
		}
		if err.Error() == "cannot remove the last admin" {
			sendErr(ctx, 400, err.Error())
			return
		}
		sendErr(ctx, 500, "failed to update role")
		return
	}
	sendJSON(ctx, 200, &u)
}

func (o *Overlay) setActive(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if c := userFrom(ctx); c != nil && c.ID == id {
		sendErr(ctx, 400, "cannot deactivate yourself")
		return
	}
	var p struct{ Active bool }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}

	var u IdentityUser
	err := o.store.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if !p.Active && u.Role == RoleAdmin {
			var count int64
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&IdentityUser{}).
				Where("role = ? AND is_active = ?", RoleAdmin, true).Count(&count).Error; err != nil {
				return err
			}
			if count <= 1 {
				return errors.New("cannot deactivate the last admin")
			}
		}
		u.IsActive = p.Active
		u.UpdatedAt = time.Now()
		return tx.Save(&u).Error
	})

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendErr(ctx, 404, "user not found")
			return
		}
		if err.Error() == "cannot deactivate the last admin" {
			sendErr(ctx, 400, err.Error())
			return
		}
		sendErr(ctx, 500, "failed to update user")
		return
	}
	sendJSON(ctx, 200, &u)
}

func (o *Overlay) setPassword(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	caller := userFrom(ctx)
	isAdmin := caller == nil || caller.Role == RoleAdmin
	isSelf := caller != nil && caller.ID == id
	if !isAdmin && !isSelf {
		sendErr(ctx, 403, "forbidden: admin or self only")
		return
	}
	u, err := o.store.GetUserByID(context.Background(), id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	var p struct{ CurrentPassword, NewPassword string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	if len(p.NewPassword) < 8 {
		sendErr(ctx, 400, "password must be at least 8 characters")
		return
	}
	if isSelf && !isAdmin {
		if u.PasswordHash == nil {
			sendErr(ctx, 400, "no password set")
			return
		}
		ok, err := compareHash(*u.PasswordHash, p.CurrentPassword)
		if err != nil {
			sendErr(ctx, 500, "error")
			return
		}
		if !ok {
			sendErr(ctx, 401, "current password is incorrect")
			return
		}
	}
	hash, err := hashPassword(p.NewPassword)
	if err != nil {
		sendErr(ctx, 500, "failed to hash password")
		return
	}
	u.PasswordHash = &hash
	if err := o.store.UpdateUser(context.Background(), u); err != nil {
		sendErr(ctx, 500, "failed to update password")
		return
	}
	_ = o.store.UnmapAllForUser(context.Background(), u.ID) // invalidate this user's sessions
	sendJSON(ctx, 200, map[string]any{"message": "password updated"})
}

func (o *Overlay) selfOrAdmin(ctx *fasthttp.RequestCtx, id string) bool {
	c := userFrom(ctx)
	return c == nil || c.Role == RoleAdmin || c.ID == id
}

func (o *Overlay) getAuthSettings(ctx *fasthttp.RequestCtx) {
	sendJSON(ctx, 200, map[string]any{
		"session_expiry_hours": o.sessionExpiryHours(),
		"is_auth_enabled":      o.authEnabled(),
	})
}

func (o *Overlay) putAuthSettings(ctx *fasthttp.RequestCtx) {
	var p struct {
		SessionExpiryHours int `json:"session_expiry_hours"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	if p.SessionExpiryHours < 1 || p.SessionExpiryHours > 8760 {
		sendErr(ctx, 400, "session_expiry_hours must be between 1 and 8760")
		return
	}

	err := o.configStore.UpdateConfig(context.Background(), &tables.TableGovernanceConfig{
		Key:   "session_expiry_hours",
		Value: fmt.Sprintf("%d", p.SessionExpiryHours),
	})
	if err != nil {
		sendErr(ctx, 500, "failed to update session expiry")
		return
	}

	sendJSON(ctx, 200, map[string]any{
		"session_expiry_hours": p.SessionExpiryHours,
		"is_auth_enabled":      o.authEnabled(),
	})
}
