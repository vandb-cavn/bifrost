package identity

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) *Store {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&IdentityUser{}, &IdentitySession{}))
	return NewStore(db)
}

func TestStore_UserAndSessionMap(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.CreateUser(ctx, &IdentityUser{ID: "u1", Email: "a@x.com", Name: "A", Role: RoleOperator, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}))

	require.NoError(t, s.MapSession(ctx, "tok", "u1"))
	u, err := s.UserForToken(ctx, "tok")
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, RoleOperator, u.Role)

	// deactivate → not resolvable
	u.IsActive = false
	require.NoError(t, s.UpdateUser(ctx, u))
	u2, err := s.UserForToken(ctx, "tok")
	require.NoError(t, err)
	assert.Nil(t, u2)

	n, err := s.CountActiveAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}
