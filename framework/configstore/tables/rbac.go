package tables

import "time"

// TableRole represents an RBAC role (system or custom).
type TableRole struct {
	ID          string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name        string    `gorm:"type:varchar(255);not null;uniqueIndex" json:"name"`
	Description string    `gorm:"type:text;not null;default:''" json:"description"`
	IsSystem    bool      `gorm:"not null;default:false" json:"is_system"`
	CreatedAt   time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableRole) TableName() string { return "governance_roles" }

// TablePermission represents a single resource+operation capability.
type TablePermission struct {
	ID        string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Resource  string `gorm:"type:varchar(100);not null;uniqueIndex:idx_perm_resource_op" json:"resource"`
	Operation string `gorm:"type:varchar(50);not null;uniqueIndex:idx_perm_resource_op" json:"operation"`
}

func (TablePermission) TableName() string { return "governance_permissions" }

// TableRolePermission is the many-to-many join between roles and permissions.
type TableRolePermission struct {
	RoleID       string `gorm:"primaryKey;type:varchar(255);index" json:"role_id"`
	PermissionID string `gorm:"primaryKey;type:varchar(255);index" json:"permission_id"`
}

func (TableRolePermission) TableName() string { return "governance_role_permissions" }

// TableUserRole assigns a role to a governance user.
type TableUserRole struct {
	UserID string `gorm:"primaryKey;type:varchar(255);index" json:"user_id"`
	RoleID string `gorm:"primaryKey;type:varchar(255);index" json:"role_id"`
}

func (TableUserRole) TableName() string { return "governance_user_roles" }
