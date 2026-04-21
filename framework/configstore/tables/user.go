package tables

import "time"

// TableUser represents a governance user with optional spending policy links.
type TableUser struct {
	ID          string  `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Email       string  `gorm:"type:varchar(255);not null;uniqueIndex" json:"email"`
	Name        string  `gorm:"type:varchar(255);not null;default:''" json:"name"`
	TeamID      *string `gorm:"type:varchar(255);index" json:"team_id,omitempty"`
	BudgetID    *string `gorm:"type:varchar(255);index" json:"budget_id,omitempty"`
	RateLimitID *string `gorm:"type:varchar(255);index" json:"rate_limit_id,omitempty"`
	AuthMethod  string  `gorm:"type:varchar(32);not null;default:'password'" json:"auth_method"`

	Team      *TableTeam      `gorm:"foreignKey:TeamID" json:"team,omitempty"`
	Budget    *TableBudget    `gorm:"foreignKey:BudgetID" json:"budget,omitempty"`
	RateLimit *TableRateLimit `gorm:"foreignKey:RateLimitID" json:"rate_limit,omitempty"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableName sets the table name for each model.
func (TableUser) TableName() string { return "governance_users" }
