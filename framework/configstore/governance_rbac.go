package configstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

// ListRoles returns all roles ordered by name.
func (s *RDBConfigStore) ListRoles(ctx context.Context) ([]*tables.TableRole, error) {
	var roles []*tables.TableRole
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

// GetRole fetches a single role by ID.
func (s *RDBConfigStore) GetRole(ctx context.Context, id string) (*tables.TableRole, error) {
	var role tables.TableRole
	if err := s.db.WithContext(ctx).First(&role, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &role, nil
}

// CreateRole inserts a new role.
func (s *RDBConfigStore) CreateRole(ctx context.Context, role *tables.TableRole) error {
	return s.db.WithContext(ctx).Create(role).Error
}

// UpdateRole updates mutable fields of a role. System roles cannot have IsSystem changed.
func (s *RDBConfigStore) UpdateRole(ctx context.Context, id string, fields map[string]any) (*tables.TableRole, error) {
	role, err := s.GetRole(ctx, id)
	if err != nil {
		return nil, err
	}
	// Whitelist: only name and description are mutable
	allowed := map[string]bool{"name": true, "description": true}
	update := map[string]any{"updated_at": time.Now().UTC()}
	for k, v := range fields {
		if allowed[k] {
			update[k] = v
		}
	}
	if err := s.db.WithContext(ctx).Model(role).Updates(update).Error; err != nil {
		return nil, err
	}
	return s.GetRole(ctx, id)
}

// DeleteRole removes a custom role. Returns an error for system roles.
func (s *RDBConfigStore) DeleteRole(ctx context.Context, id string) error {
	role, err := s.GetRole(ctx, id)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("cannot delete system role %q", role.Name)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", id).Delete(&tables.TableRolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("role_id = ?", id).Delete(&tables.TableUserRole{}).Error; err != nil {
			return err
		}
		return tx.Delete(&tables.TableRole{}, "id = ?", id).Error
	})
}

// GetRolePermissions returns all permissions assigned to a role.
func (s *RDBConfigStore) GetRolePermissions(ctx context.Context, roleID string) ([]*tables.TablePermission, error) {
	var perms []*tables.TablePermission
	err := s.db.WithContext(ctx).
		Joins("JOIN governance_role_permissions rp ON rp.permission_id = governance_permissions.id").
		Where("rp.role_id = ?", roleID).
		Find(&perms).Error
	return perms, err
}

// SetRolePermissions replaces the full permission set for a role in a single transaction.
func (s *RDBConfigStore) SetRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&tables.TableRolePermission{}).Error; err != nil {
			return err
		}
		for _, pid := range permissionIDs {
			row := &tables.TableRolePermission{RoleID: roleID, PermissionID: pid}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListPermissions returns all permissions in the system.
func (s *RDBConfigStore) ListPermissions(ctx context.Context) ([]*tables.TablePermission, error) {
	var perms []*tables.TablePermission
	if err := s.db.WithContext(ctx).Order("resource ASC, operation ASC").Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

// AssignUserRole gives a user a role. Silently no-ops if already assigned.
func (s *RDBConfigStore) AssignUserRole(ctx context.Context, userID, roleID string) error {
	row := &tables.TableUserRole{UserID: userID, RoleID: roleID}
	return s.db.WithContext(ctx).FirstOrCreate(row, "user_id = ? AND role_id = ?", userID, roleID).Error
}

// RemoveUserRole removes a role from a user.
func (s *RDBConfigStore) RemoveUserRole(ctx context.Context, userID, roleID string) error {
	return s.db.WithContext(ctx).
		Where("user_id = ? AND role_id = ?", userID, roleID).
		Delete(&tables.TableUserRole{}).Error
}

// GetUserRoles returns all roles assigned to a user.
func (s *RDBConfigStore) GetUserRoles(ctx context.Context, userID string) ([]*tables.TableRole, error) {
	var roles []*tables.TableRole
	err := s.db.WithContext(ctx).
		Joins("JOIN governance_user_roles ur ON ur.role_id = governance_roles.id").
		Where("ur.user_id = ?", userID).
		Find(&roles).Error
	return roles, err
}

// GetUserPermissions returns the merged, deduplicated permission set across all of a user's roles.
func (s *RDBConfigStore) GetUserPermissions(ctx context.Context, userID string) ([]*tables.TablePermission, error) {
	var perms []*tables.TablePermission
	err := s.db.WithContext(ctx).
		Distinct("governance_permissions.id", "governance_permissions.resource", "governance_permissions.operation").
		Joins("JOIN governance_role_permissions rp ON rp.permission_id = governance_permissions.id").
		Joins("JOIN governance_user_roles ur ON ur.role_id = rp.role_id").
		Where("ur.user_id = ?", userID).
		Find(&perms).Error
	return perms, err
}
