package identity

import (
	"context"
	"errors"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
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

// UnmapAllForUser removes all session maps for a user (password change / deactivation).
func (s *Store) UnmapAllForUser(ctx context.Context, userID string) error {
	return s.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&IdentitySession{}).Error
}
