package identity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestCreateUser_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)

	// 1. Password too short
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"email":"test@example.com","name":"Test","role":"viewer","password":"short"}`))
		o.createUser(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "password must be at least 8 characters")
	}

	// 2. Invalid role
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"email":"test@example.com","name":"Test","role":"superadmin","password":"password123"}`))
		o.createUser(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "role must be one of")
	}

	// 3. Invalid email
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"email":"invalid-email","name":"Test","role":"viewer","password":"password123"}`))
		o.createUser(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "invalid email")
	}

	// 4. Successful creation
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"email":"test@example.com","name":"Test","role":"viewer","password":"password123"}`))
		o.createUser(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())

		var res IdentityUser
		require.NoError(t, json.Unmarshal(rc.Response.Body(), &res))
		assert.Equal(t, "test@example.com", res.Email)
		assert.Equal(t, "Test", res.Name)
		assert.Equal(t, RoleViewer, res.Role)
		assert.True(t, res.IsActive)
	}

	// 5. Duplicate email
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"email":"test@example.com","name":"Test2","role":"viewer","password":"password123"}`))
		o.createUser(rc)
		assert.Equal(t, 409, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "email already in use")
	}
}

func TestUpdateUser_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)
	ctx := context.Background()

	u := &IdentityUser{ID: "u1", Email: "u1@x.com", Name: "U1", Role: RoleViewer, IsActive: true}
	require.NoError(t, o.store.CreateUser(ctx, u))

	// 1. Forbidden for other non-admin user
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "u1")
		rc.SetUserValue(ctxKeyUser, &IdentityUser{ID: "u2", Role: RoleViewer})
		o.updateUser(rc)
		assert.Equal(t, 403, rc.Response.StatusCode())
	}

	// 2. Allowed for self
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "u1")
		rc.SetUserValue(ctxKeyUser, &IdentityUser{ID: "u1", Role: RoleViewer})
		rc.Request.SetBody([]byte(`{"name":"U1 Updated","email":"u1_new@x.com","role":"admin"}`))
		o.updateUser(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())

		// Verify role did NOT change (ignored from body)
		dbUser, err := o.store.GetUserByID(ctx, "u1")
		require.NoError(t, err)
		assert.Equal(t, "U1 Updated", dbUser.Name)
		assert.Equal(t, "u1_new@x.com", dbUser.Email)
		assert.Equal(t, RoleViewer, dbUser.Role) // unchanged!
	}
}

func TestSetRole_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)
	ctx := context.Background()

	// Seed single admin
	admin := &IdentityUser{ID: "admin1", Email: "admin@x.com", Name: "Admin", Role: RoleAdmin, IsActive: true}
	require.NoError(t, o.store.CreateUser(ctx, admin))

	// Seed viewer
	viewer := &IdentityUser{ID: "viewer1", Email: "viewer@x.com", Name: "Viewer", Role: RoleViewer, IsActive: true}
	require.NoError(t, o.store.CreateUser(ctx, viewer))

	// 1. Cannot change own role
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "admin1")
		rc.SetUserValue(ctxKeyUser, admin)
		rc.Request.SetBody([]byte(`{"role":"viewer"}`))
		o.setRole(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "cannot change your own role")
	}

	// 2. Cannot demote last admin
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "admin1")
		rc.SetUserValue(ctxKeyUser, &IdentityUser{ID: "other_admin", Role: RoleAdmin}) // some other admin calling
		rc.Request.SetBody([]byte(`{"role":"viewer"}`))
		o.setRole(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "cannot remove the last admin")
	}

	// 3. Can demote admin if another admin exists
	{
		otherAdmin := &IdentityUser{ID: "admin2", Email: "admin2@x.com", Name: "Admin2", Role: RoleAdmin, IsActive: true}
		require.NoError(t, o.store.CreateUser(ctx, otherAdmin))

		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "admin1")
		rc.SetUserValue(ctxKeyUser, otherAdmin)
		rc.Request.SetBody([]byte(`{"role":"operator"}`))
		o.setRole(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())

		dbUser, err := o.store.GetUserByID(ctx, "admin1")
		require.NoError(t, err)
		assert.Equal(t, RoleOperator, dbUser.Role)
	}
}

func TestSetActive_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)
	ctx := context.Background()

	admin := &IdentityUser{ID: "admin1", Email: "admin@x.com", Name: "Admin", Role: RoleAdmin, IsActive: true}
	require.NoError(t, o.store.CreateUser(ctx, admin))

	// 1. Cannot deactivate self
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "admin1")
		rc.SetUserValue(ctxKeyUser, admin)
		rc.Request.SetBody([]byte(`{"active":false}`))
		o.setActive(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "cannot deactivate yourself")
	}

	// 2. Cannot deactivate last admin
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "admin1")
		rc.SetUserValue(ctxKeyUser, &IdentityUser{ID: "other", Role: RoleAdmin})
		rc.Request.SetBody([]byte(`{"active":false}`))
		o.setActive(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "cannot deactivate the last admin")
	}
}

func TestSetPassword_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)
	ctx := context.Background()

	hash, _ := encrypt.Hash("oldpassword")
	u := &IdentityUser{ID: "u1", Email: "u1@x.com", Name: "U1", Role: RoleViewer, IsActive: true, PasswordHash: &hash}
	require.NoError(t, o.store.CreateUser(ctx, u))

	// 1. Self password update requires correct current password
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "u1")
		rc.SetUserValue(ctxKeyUser, u)
		rc.Request.SetBody([]byte(`{"currentPassword":"wrong","newPassword":"newpassword123"}`))
		o.setPassword(rc)
		assert.Equal(t, 401, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "current password is incorrect")
	}

	// 2. Self password update successful with correct password
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "u1")
		rc.SetUserValue(ctxKeyUser, u)
		rc.Request.SetBody([]byte(`{"currentPassword":"oldpassword","newPassword":"newpassword123"}`))
		o.setPassword(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())

		dbUser, err := o.store.GetUserByID(ctx, "u1")
		require.NoError(t, err)
		ok, err := encrypt.CompareHash(*dbUser.PasswordHash, "newpassword123")
		require.NoError(t, err)
		assert.True(t, ok)
	}

	// 3. Admin can update anyone's password without current password
	{
		rc := &fasthttp.RequestCtx{}
		rc.SetUserValue("id", "u1")
		rc.SetUserValue(ctxKeyUser, &IdentityUser{ID: "admin", Role: RoleAdmin})
		rc.Request.SetBody([]byte(`{"newPassword":"adminpassword123"}`))
		o.setPassword(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())

		dbUser, err := o.store.GetUserByID(ctx, "u1")
		require.NoError(t, err)
		ok, err := encrypt.CompareHash(*dbUser.PasswordHash, "adminpassword123")
		require.NoError(t, err)
		assert.True(t, ok)
	}
}

func TestAuthSettings_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)

	// 1. GET settings
	{
		rc := &fasthttp.RequestCtx{}
		o.getAuthSettings(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), `"session_expiry_hours":720`)
	}

	// 2. PUT settings range validation (too low)
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"session_expiry_hours":0}`))
		o.putAuthSettings(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "must be between 1 and 8760")
	}

	// 3. PUT settings range validation (too high)
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"session_expiry_hours":10000}`))
		o.putAuthSettings(rc)
		assert.Equal(t, 400, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), "must be between 1 and 8760")
	}

	// 4. PUT settings success
	{
		rc := &fasthttp.RequestCtx{}
		rc.Request.SetBody([]byte(`{"session_expiry_hours":240}`))
		o.putAuthSettings(rc)
		assert.Equal(t, 200, rc.Response.StatusCode())
		assert.Contains(t, string(rc.Response.Body()), `"session_expiry_hours":240`)
	}
}
