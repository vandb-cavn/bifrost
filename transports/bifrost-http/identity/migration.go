package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// Migrate creates the overlay tables and, on first run, migrates the legacy
// single admin from governance_config and backfills existing core sessions so
// the current admin is NOT logged out. Idempotent.
func Migrate(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	m := db.Migrator()
	if !m.HasTable(&IdentityUser{}) {
		if err := m.CreateTable(&IdentityUser{}); err != nil {
			return err
		}
	}
	if !m.HasTable(&IdentitySession{}) {
		if err := m.CreateTable(&IdentitySession{}); err != nil {
			return err
		}
	}

	// Already migrated? Check the migration marker in governance_config.
	var marker tables.TableGovernanceConfig
	err := db.First(&marker, "key = ?", "identity_migrated").Error
	if err == nil && marker.Value == "true" {
		return nil // already migrated
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// Read legacy admin from governance_config.
	var username, password *string
	if err := db.First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigAdminUsernameKey).Select("value").Scan(&username).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if err := db.First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigAdminPasswordKey).Select("value").Scan(&password).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if username == nil || password == nil || *username == "" || *password == "" {
		// Branch B: no legacy admin (auth disabled / fresh) — tables ready, nothing to seed.
		// Still write the migration marker so we don't try to seed later if legacy config gets populated.
		mRow := tables.TableGovernanceConfig{Key: "identity_migrated", Value: "true"}
		if err := db.Save(&mRow).Error; err != nil {
			return err
		}
		return nil
	}

	adminID := uuid.New().String()
	pw := *password // already a bcrypt hash — DO NOT re-hash
	now := time.Now()
	admin := IdentityUser{ID: adminID, Email: "admin@localhost", Name: *username, Role: RoleAdmin, PasswordHash: &pw, IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&admin).Error; err != nil {
		return err
	}

	// Backfill: map every existing core session to the migrated admin so the
	// current admin stays logged in. We read the core sessions table directly.
	var sessions []tables.SessionsTable
	if err := db.Find(&sessions).Error; err != nil {
		return err
	}
	for _, s := range sessions {
		if s.TokenHash == "" {
			continue // legacy plaintext-only session; cannot map, will require re-login
		}
		row := IdentitySession{TokenHash: s.TokenHash, UserID: adminID, CreatedAt: now}
		if err := db.Where("token_hash = ?", s.TokenHash).FirstOrCreate(&row).Error; err != nil {
			return err
		}
	}

	// Mark as migrated
	mRow := tables.TableGovernanceConfig{Key: "identity_migrated", Value: "true"}
	if err := db.Save(&mRow).Error; err != nil {
		return err
	}

	return nil
}

var _ = encrypt.HashSHA256 // encrypt used by Store (kept consistent across files)
