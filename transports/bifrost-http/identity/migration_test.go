package identity

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&tables.TableGovernanceConfig{}, &tables.SessionsTable{}))
	return db
}

func TestMigrate_BranchA_MigratesAdmin_AndBackfillsSession(t *testing.T) {
	db := newDB(t)
	hash, err := encrypt.Hash("secret123")
	require.NoError(t, err)
	require.NoError(t, db.Create(&tables.TableGovernanceConfig{Key: tables.ConfigAdminUsernameKey, Value: "rootadmin"}).Error)
	require.NoError(t, db.Create(&tables.TableGovernanceConfig{Key: tables.ConfigAdminPasswordKey, Value: hash}).Error)
	// existing core session (BeforeSave sets token_hash from the token)
	require.NoError(t, db.Create(&tables.SessionsTable{Token: "tok", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)

	require.NoError(t, Migrate(context.Background(), db))

	var admin IdentityUser
	require.NoError(t, db.First(&admin, "email = ?", "admin@localhost").Error)
	assert.Equal(t, RoleAdmin, admin.Role)
	ok, err := encrypt.CompareHash(*admin.PasswordHash, "secret123") // verbatim copy, no double-hash
	require.NoError(t, err)
	assert.True(t, ok)

	th := encrypt.HashSHA256("tok")
	var mapped IdentitySession
	require.NoError(t, db.First(&mapped, "token_hash = ?", th).Error)
	assert.Equal(t, admin.ID, mapped.UserID)

	// idempotent
	require.NoError(t, Migrate(context.Background(), db))
	var n int64
	require.NoError(t, db.Model(&IdentityUser{}).Count(&n).Error)
	assert.Equal(t, int64(1), n)
}
