package governance

import "testing"

func TestRedisCounterKeys(t *testing.T) {
	tests := []struct {
		name     string
		actual   string
		expected string
	}{
		{
			name:     "Rate limit tokens key format",
			actual:   rlTokenKey("X"),
			expected: "bifrost:rl:X:tokens",
		},
		{
			name:     "Rate limit requests key format",
			actual:   rlRequestKey("X"),
			expected: "bifrost:rl:X:requests",
		},
		{
			name:     "Budget spent key format",
			actual:   budgetKey("X"),
			expected: "bifrost:budget:X:spent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.actual != tt.expected {
				t.Errorf("Expected key %q, got %q", tt.expected, tt.actual)
			}
		})
	}
}
