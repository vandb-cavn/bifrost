package identity

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type fakeConfigStore struct {
	configstore.ConfigStore
	sessions map[string]*tables.SessionsTable
}

func (f *fakeConfigStore) CreateSession(ctx context.Context, s *tables.SessionsTable) error {
	f.sessions[encrypt.HashSHA256(s.Token)] = s
	return nil
}

func (f *fakeConfigStore) GetAuthConfig(ctx context.Context) (*configstore.AuthConfig, error) {
	return &configstore.AuthConfig{IsEnabled: true}, nil
}

func (f *fakeConfigStore) UpdateConfig(ctx context.Context, config *tables.TableGovernanceConfig, tx ...*gorm.DB) error {
	return nil
}

func newOverlayUnderTest(t *testing.T) *Overlay {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&IdentityUser{}, &IdentitySession{}))
	store := NewStore(db)
	fakeCS := &fakeConfigStore{
		sessions: make(map[string]*tables.SessionsTable),
	}
	return &Overlay{
		store:       store,
		configStore: fakeCS,
		authEnabled: func() bool { return true },
	}
}

func TestIdentityMiddleware_LoginIntercept(t *testing.T) {
	o := newOverlayUnderTest(t)
	ctx := context.Background()
	hash, _ := encrypt.Hash("secret123")
	require.NoError(t, o.store.CreateUser(ctx, &IdentityUser{ID: "a1", Email: "admin@localhost", Name: "A", Role: RoleAdmin, PasswordHash: &hash, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}))

	coreCalled := false
	h := o.IdentityMiddleware()(func(c *fasthttp.RequestCtx) { coreCalled = true })

	rc := &fasthttp.RequestCtx{}
	rc.Request.Header.SetMethod("POST")
	rc.Request.SetRequestURI("/api/session/login")
	rc.Request.SetBody([]byte(`{"email":"admin@localhost","password":"secret123"}`))
	h(rc)

	assert.False(t, coreCalled) // core login handler bypassed
	assert.Equal(t, 200, rc.Response.StatusCode())
	assert.Contains(t, string(rc.Response.Body()), `"role":"admin"`)
}
