package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store is the overlay's persistence over the shared DB. Obtain *gorm.DB by
// type-asserting the ConfigStore to *RDBConfigStore (see wire.go).
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) GetUserByID(ctx context.Context, id string) (*IdentityUser, error) {
	var u IdentityUser
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*IdentityUser, error) {
	var u IdentityUser
	if err := s.db.WithContext(ctx).First(&u, "email = ?", email).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]IdentityUser, error) {
	var users []IdentityUser
	return users, s.db.WithContext(ctx).Order("created_at asc").Find(&users).Error
}

func (s *Store) CreateUser(ctx context.Context, u *IdentityUser) error {
	return s.db.WithContext(ctx).Create(u).Error
}

func (s *Store) UpdateUser(ctx context.Context, u *IdentityUser) error {
	u.UpdatedAt = time.Now()
	return s.db.WithContext(ctx).Save(u).Error
}

func (s *Store) CountActiveAdmins(ctx context.Context) (int64, error) {
	var n int64
	return n, s.db.WithContext(ctx).Model(&IdentityUser{}).
		Where("role = ? AND is_active = ?", RoleAdmin, true).Count(&n).Error
}

// MapSession records token_hash → user_id (called by our login handler).
func (s *Store) MapSession(ctx context.Context, token, userID string) error {
	return s.db.WithContext(ctx).Create(&IdentitySession{
		TokenHash: encrypt.HashSHA256(token), UserID: userID, CreatedAt: time.Now(),
	}).Error
}

// UserForToken resolves an active user from a session token via the map.
func (s *Store) UserForToken(ctx context.Context, token string) (*IdentityUser, error) {
	var m IdentitySession
	if err := s.db.WithContext(ctx).First(&m, "token_hash = ?", encrypt.HashSHA256(token)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	u, err := s.GetUserByID(ctx, m.UserID)
	if err != nil || u == nil || !u.IsActive {
		return nil, err
	}
	return u, nil
}

// UnmapAllForUser removes all session maps and core sessions for a user (password change / deactivation).
func (s *Store) UnmapAllForUser(ctx context.Context, userID string) error {
	var sessions []IdentitySession
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&sessions).Error; err == nil {
		for _, sess := range sessions {
			_ = s.db.WithContext(ctx).Where("token_hash = ?", sess.TokenHash).Delete(&tables.SessionsTable{})
		}
	}
	return s.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&IdentitySession{}).Error
}

// UnmapAllButCurrentForUser removes all session maps and core sessions for a user EXCEPT the current session.
func (s *Store) UnmapAllButCurrentForUser(ctx context.Context, userID, currentToken string) error {
	currentHash := encrypt.HashSHA256(currentToken)
	var sessions []IdentitySession
	if err := s.db.WithContext(ctx).Where("user_id = ? AND token_hash != ?", userID, currentHash).Find(&sessions).Error; err == nil {
		for _, sess := range sessions {
			_ = s.db.WithContext(ctx).Where("token_hash = ?", sess.TokenHash).Delete(&tables.SessionsTable{})
		}
	}
	return s.db.WithContext(ctx).Where("user_id = ? AND token_hash != ?", userID, currentHash).Delete(&IdentitySession{}).Error
}

// SetRoleTx updates user role inside a transaction with Postgres row locking.
func (s *Store) SetRoleTx(ctx context.Context, id string, newRole string) (*IdentityUser, error) {
	var u IdentityUser
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if u.Role == RoleAdmin && newRole != RoleAdmin {
			var count int64
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&IdentityUser{}).
				Where("role = ? AND is_active = ?", RoleAdmin, true).Count(&count).Error; err != nil {
				return err
			}
			if count <= 1 {
				return errors.New("cannot remove the last admin")
			}
		}
		u.Role = newRole
		u.UpdatedAt = time.Now()
		return tx.Save(&u).Error
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SetActiveTx updates user active status inside a transaction with Postgres row locking.
func (s *Store) SetActiveTx(ctx context.Context, id string, active bool) (*IdentityUser, error) {
	var u IdentityUser
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if !active && u.Role == RoleAdmin {
			var count int64
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&IdentityUser{}).
				Where("role = ? AND is_active = ?", RoleAdmin, true).Count(&count).Error; err != nil {
				return err
			}
			if count <= 1 {
				return errors.New("cannot deactivate the last admin")
			}
		}
		u.IsActive = active
		u.UpdatedAt = time.Now()
		return tx.Save(&u).Error
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetSessionExpiryHours retrieves the session expiry configuration from governance_config.
func (s *Store) GetSessionExpiryHours(ctx context.Context) int {
	var val string
	err := s.db.WithContext(ctx).Table("governance_config").Where("key = ?", "session_expiry_hours").Select("value").Scan(&val).Error
	if err == nil && val != "" {
		var hrs int
		if _, err := fmt.Sscanf(val, "%d", &hrs); err == nil && hrs > 0 {
			return hrs
		}
	}
	return 720
}
