package identity

import "time"

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var ValidRoles = map[string]bool{RoleAdmin: true, RoleOperator: true, RoleViewer: true}

// IdentityUser is a named dashboard user (fork-owned; does not touch core tables).
type IdentityUser struct {
	ID           string     `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Email        string     `gorm:"type:varchar(255);not null;uniqueIndex" json:"email"`
	Name         string     `gorm:"type:varchar(255);not null" json:"name"`
	Role         string     `gorm:"type:varchar(50);not null;default:'viewer'" json:"role"`
	PasswordHash *string    `gorm:"type:text" json:"-"`
	IsActive     bool       `gorm:"not null;default:true" json:"is_active"`
	LastLoginAt  *time.Time `gorm:"index" json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `gorm:"index;not null" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"not null" json:"updated_at"`
}

func (IdentityUser) TableName() string { return "identity_users" }

// IdentitySession maps a core session token (by its SHA-256 hash) to a user.
// We mirror the core token_hash so we never store the raw token and can look
// up the user for a request that core's AuthMiddleware already authenticated.
type IdentitySession struct {
	TokenHash string    `gorm:"primaryKey;type:varchar(64)" json:"-"`
	UserID    string    `gorm:"type:varchar(255);index;not null" json:"user_id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (IdentitySession) TableName() string { return "identity_sessions" }
