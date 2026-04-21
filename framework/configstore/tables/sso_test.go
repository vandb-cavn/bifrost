package tables

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableGovernanceSSOConfig_AllowedGroups(t *testing.T) {
	cfg := &TableGovernanceSSOConfig{}

	// Initially empty
	groups, err := cfg.GetAllowedGroups()
	require.NoError(t, err)
	assert.Nil(t, groups)

	// Set groups
	cfg.SetAllowedGroups([]string{" Admin ", "DevOps", "admin", ""})
	assert.Equal(t, `["admin","devops"]`, cfg.AllowedGroups)

	// Get groups
	groups, err = cfg.GetAllowedGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"admin", "devops"}, groups)

	// Set empty
	cfg.SetAllowedGroups([]string{" ", ""})
	assert.Equal(t, "", cfg.AllowedGroups)
	groups, err = cfg.GetAllowedGroups()
	require.NoError(t, err)
	assert.Nil(t, groups)

	// Malformed JSON
	cfg.AllowedGroups = "not-json"
	groups, err = cfg.GetAllowedGroups()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "malformed")
	assert.Nil(t, groups)
}
