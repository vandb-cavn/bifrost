package tables

import "time"

// TableGuardrailRule defines when and how content is evaluated for policy violations.
type TableGuardrailRule struct {
	ID            string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name          string    `gorm:"type:varchar(255);not null" json:"name"`
	Description   string    `gorm:"type:text" json:"description"`
	Enabled       bool      `gorm:"not null;default:true" json:"enabled"`
	CelExpression string    `gorm:"type:text;not null" json:"cel_expression"`
	ApplyTo       string    `gorm:"type:varchar(10);not null" json:"apply_to"` // "input"|"output"|"both"
	Action        string    `gorm:"type:varchar(10);not null" json:"action"`   // "block"|"warn"
	SamplingRate  int       `gorm:"not null;default:100" json:"sampling_rate"` // 0–100
	TimeoutMs     int       `gorm:"not null;default:5000" json:"timeout_ms"`
	Priority      int       `gorm:"type:int;not null;default:0;index" json:"priority"`
	Scope         string    `gorm:"type:varchar(50);not null" json:"scope"` // "global"|"virtual_key"|"team"
	ScopeID       *string   `gorm:"type:varchar(255)" json:"scope_id"`
	BlockMessage  string    `gorm:"type:text" json:"block_message"`
	FailOpen      bool      `gorm:"not null;default:true" json:"fail_open"`

	// many-to-many via guardrail_rule_profiles join table; delete rule → delete join rows
	Profiles []TableGuardrailProfile `gorm:"many2many:guardrail_rule_profiles;constraint:OnDelete:CASCADE" json:"profiles,omitempty"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableGuardrailRule) TableName() string { return "guardrail_rules" }
