package tables

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// TableGuardrailProfile stores credentials for an external content-safety provider.
// ConfigJSON is encrypted at rest using the same pattern as TablePlugin.
type TableGuardrailProfile struct {
	ID               string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name             string    `gorm:"type:varchar(255);not null" json:"name"`
	ProviderName     string    `gorm:"type:varchar(50);not null" json:"provider_name"` // "bedrock"|"azure"|...
	Enabled          bool      `gorm:"not null;default:true" json:"enabled"`
	ConfigJSON       string    `gorm:"type:text" json:"-"`
	EncryptionStatus string    `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
	CreatedAt        time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableGuardrailProfile) TableName() string { return "guardrail_profiles" }

// BeforeSave encrypts ConfigJSON if encryption is enabled.
func (p *TableGuardrailProfile) BeforeSave(tx *gorm.DB) error {
	if encrypt.IsEnabled() && p.ConfigJSON != "" && p.ConfigJSON != "{}" {
		encrypted, err := encrypt.Encrypt(p.ConfigJSON)
		if err != nil {
			return fmt.Errorf("failed to encrypt guardrail profile config: %w", err)
		}
		p.ConfigJSON = encrypted
		p.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

// AfterFind decrypts ConfigJSON if it was encrypted.
func (p *TableGuardrailProfile) AfterFind(tx *gorm.DB) error {
	if p.EncryptionStatus == EncryptionStatusEncrypted && p.ConfigJSON != "" {
		decrypted, err := encrypt.Decrypt(p.ConfigJSON)
		if err != nil {
			return fmt.Errorf("failed to decrypt guardrail profile config: %w", err)
		}
		p.ConfigJSON = decrypted
	}
	return nil
}
