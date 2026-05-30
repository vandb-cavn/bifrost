package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func allowed(role, method, path string) bool { return roleRank(role) >= requiredRank(method, path) }

func TestRBACMatrix(t *testing.T) {
	cases := []struct {
		role, method, path string
		want               bool
	}{
		{RoleOperator, "GET", "/api/users", false},
		{RoleOperator, "POST", "/api/users", false},
		{RoleOperator, "PUT", "/api/config", false},        // SECURITY: cannot disable auth
		{RoleOperator, "POST", "/api/brand-new-thing", false}, // fail-closed
		{RoleAdmin, "POST", "/api/brand-new-thing", true},
		{RoleViewer, "GET", "/api/providers", true},
		{RoleViewer, "POST", "/api/providers", false},
		{RoleOperator, "POST", "/api/providers", true},
		{RoleViewer, "GET", "/api/users/me", true},
		{RoleOperator, "GET", "/api/users/u123", true},
		{RoleOperator, "PUT", "/api/users/u123/role", false},
		{RoleAdmin, "PUT", "/api/users/u123/role", true},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, allowed(c.role, c.method, c.path), "%s %s %s", c.role, c.method, c.path)
	}
}
