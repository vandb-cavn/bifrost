package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// TableGovernanceSSOConfig stores a single OIDC provider configuration for dashboard login.
type TableGovernanceSSOConfig struct {
	ID               string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Provider         string    `gorm:"type:varchar(50);not null" json:"provider"`
	IssuerURL        string    `gorm:"type:text;not null" json:"issuer_url"`
	ClientID         string    `gorm:"type:text;not null" json:"client_id"`
	ClientSecret     string    `gorm:"type:text;not null" json:"-"`
	RoleClaimKey     string    `gorm:"type:varchar(255);not null;default:''" json:"role_claim_key"`
	GroupClaimKey    string    `gorm:"type:varchar(255);not null;default:''" json:"group_claim_key"`
	AllowedGroups    string    `gorm:"type:text;not null;default:''" json:"-"`
	Enabled          bool      `gorm:"not null;default:false" json:"enabled"`
	EncryptionStatus string    `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
	CreatedAt        time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableName sets the table name for each model.
func (TableGovernanceSSOConfig) TableName() string { return "governance_sso_configs" }

// GetAllowedGroups returns the parsed allowed groups list.
// Returns an error if AllowedGroups is non-empty but malformed — callers must
// treat this as a deny (fail-closed) to prevent bypassing the filter.
func (c *TableGovernanceSSOConfig) GetAllowedGroups() ([]string, error) {
	if c.AllowedGroups == "" {
		return nil, nil
	}
	var groups []string
	if err := json.Unmarshal([]byte(c.AllowedGroups), &groups); err != nil {
		return nil, fmt.Errorf("allowed_groups is malformed: %w", err)
	}
	return groups, nil
}

func (c *TableGovernanceSSOConfig) SetAllowedGroups(groups []string) {
	seen := make(map[string]bool)
	sanitized := make([]string, 0, len(groups))
	for _, g := range groups {
		g = strings.ToLower(strings.TrimSpace(g))
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		sanitized = append(sanitized, g)
	}
	if len(sanitized) == 0 {
		c.AllowedGroups = ""
		return
	}
	b, _ := json.Marshal(sanitized)
	c.AllowedGroups = string(b)
}

// BeforeSave encrypts the client secret when encryption is enabled.
func (c *TableGovernanceSSOConfig) BeforeSave(tx *gorm.DB) error {
	if encrypt.IsEnabled() && c.ClientSecret != "" {
		if err := encryptString(&c.ClientSecret); err != nil {
			return fmt.Errorf("failed to encrypt governance sso client secret: %w", err)
		}
		c.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

// AfterFind decrypts the client secret when it is stored encrypted.
func (c *TableGovernanceSSOConfig) AfterFind(tx *gorm.DB) error {
	if c.EncryptionStatus == EncryptionStatusEncrypted {
		if err := decryptString(&c.ClientSecret); err != nil {
			return fmt.Errorf("failed to decrypt governance sso client secret: %w", err)
		}
	}
	return nil
}

// TableGovernanceSSONonce stores the PKCE verifier for an in-flight OIDC login.
type TableGovernanceSSONonce struct {
	State            string    `gorm:"primaryKey;type:varchar(255)" json:"state"`
	CodeVerifier     string    `gorm:"type:text;not null" json:"code_verifier"`
	Nonce            string    `gorm:"type:varchar(255);not null" json:"nonce"`
	Provider         string    `gorm:"type:varchar(50);not null;index" json:"provider"`
	ExpiresAt        time.Time `gorm:"index:idx_governance_sso_nonces_expires;not null" json:"expires_at"`
	EncryptionStatus string    `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
}

// TableName sets the table name for each model.
func (TableGovernanceSSONonce) TableName() string { return "governance_sso_nonces" }

// BeforeSave encrypts the code verifier when encryption is enabled.
func (n *TableGovernanceSSONonce) BeforeSave(tx *gorm.DB) error {
	if encrypt.IsEnabled() && n.CodeVerifier != "" {
		if err := encryptString(&n.CodeVerifier); err != nil {
			return fmt.Errorf("failed to encrypt governance sso code verifier: %w", err)
		}
		n.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

// AfterFind decrypts the code verifier when it is stored encrypted.
func (n *TableGovernanceSSONonce) AfterFind(tx *gorm.DB) error {
	if n.EncryptionStatus == EncryptionStatusEncrypted {
		if err := decryptString(&n.CodeVerifier); err != nil {
			return fmt.Errorf("failed to decrypt governance sso code verifier: %w", err)
		}
	}
	return nil
}
