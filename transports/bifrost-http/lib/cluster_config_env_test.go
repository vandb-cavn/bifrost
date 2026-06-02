package lib

import (
	"encoding/json"
	"os"
	"testing"
)

func TestClusterConfigEnv(t *testing.T) {
	// 1. os.Setenv("REDIS_PASSWORD","s3cr3t"); unmarshal a ClusterRedisConfig JSON
	// with "password":"env.REDIS_PASSWORD"; assert Password.GetValue()=="s3cr3t".
	t.Run("EnvVar Password Resolution", func(t *testing.T) {
		err := os.Setenv("REDIS_PASSWORD", "s3cr3t")
		if err != nil {
			t.Fatalf("failed to set env: %v", err)
		}
		defer os.Unsetenv("REDIS_PASSWORD")

		jsonData := `{"password": "env.REDIS_PASSWORD"}`
		var cfg ClusterRedisConfig
		if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if got := cfg.Password.GetValue(); got != "s3cr3t" {
			t.Errorf("expected Password GetValue() = 's3cr3t', got %q", got)
		}

		// 2. Password.Redacted().GetValue() does NOT contain "s3cr3t".
		redacted := cfg.Password.Redacted()
		if redacted.GetValue() == "s3cr3t" {
			t.Errorf("expected Redacted value NOT to contain 's3cr3t'")
		}
	})

	// 3. os.Setenv("REDIS_ADDR","r:6379"); "addr":"env.REDIS_ADDR" resolves via GetValue().
	t.Run("EnvVar Addr Resolution", func(t *testing.T) {
		err := os.Setenv("REDIS_ADDR", "r:6379")
		if err != nil {
			t.Fatalf("failed to set env: %v", err)
		}
		defer os.Unsetenv("REDIS_ADDR")

		jsonData := `{"addr": "env.REDIS_ADDR"}`
		var cfg ClusterRedisConfig
		if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if got := cfg.Addr.GetValue(); got != "r:6379" {
			t.Errorf("expected Addr GetValue() = 'r:6379', got %q", got)
		}
	})

	// 4. A literal "password":"plainpw" still yields GetValue()=="plainpw" (back-compat).
	t.Run("Literal Password Backward Compatibility", func(t *testing.T) {
		jsonData := `{"password": "plainpw"}`
		var cfg ClusterRedisConfig
		if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if got := cfg.Password.GetValue(); got != "plainpw" {
			t.Errorf("expected Password GetValue() = 'plainpw', got %q", got)
		}
	})

	// 5. Addrs []schemas.EnvVar resolution
	t.Run("EnvVar Addrs Resolution", func(t *testing.T) {
		err1 := os.Setenv("REDIS_ADDR_1", "r1:6379")
		err2 := os.Setenv("REDIS_ADDR_2", "r2:6379")
		if err1 != nil || err2 != nil {
			t.Fatalf("failed to set envs")
		}
		defer os.Unsetenv("REDIS_ADDR_1")
		defer os.Unsetenv("REDIS_ADDR_2")

		jsonData := `{"addrs": ["env.REDIS_ADDR_1", "env.REDIS_ADDR_2", "literal:6379"]}`
		var cfg ClusterRedisConfig
		if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if len(cfg.Addrs) != 3 {
			t.Fatalf("expected 3 addrs, got %d", len(cfg.Addrs))
		}

		if got := cfg.Addrs[0].GetValue(); got != "r1:6379" {
			t.Errorf("expected index 0 to be 'r1:6379', got %q", got)
		}
		if got := cfg.Addrs[1].GetValue(); got != "r2:6379" {
			t.Errorf("expected index 1 to be 'r2:6379', got %q", got)
		}
		if got := cfg.Addrs[2].GetValue(); got != "literal:6379" {
			t.Errorf("expected index 2 to be 'literal:6379', got %q", got)
		}
	})
}

func TestConsumerID(t *testing.T) {
	// A ClusterConfig with "node_id":"env.MY_NODE" (env set) -> ConsumerID()=="<that value>";
	// with node_id empty + BIFROST_NODE_ID set -> ConsumerID()==BIFROST_NODE_ID.
	t.Run("ConsumerID priority", func(t *testing.T) {
		// Clear environment variables that might interfere
		bNodeId := os.Getenv("BIFROST_NODE_ID")
		defer func() {
			if bNodeId != "" {
				os.Setenv("BIFROST_NODE_ID", bNodeId)
			} else {
				os.Unsetenv("BIFROST_NODE_ID")
			}
		}()
		os.Unsetenv("BIFROST_NODE_ID")

		// 1. With NodeID set via env resolution
		err := os.Setenv("MY_NODE", "resolved-node-id")
		if err != nil {
			t.Fatalf("failed to set env: %v", err)
		}
		defer os.Unsetenv("MY_NODE")

		jsonData := `{"node_id": "env.MY_NODE"}`
		var cfg ClusterConfig
		if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if got := cfg.ConsumerID(); got != "resolved-node-id" {
			t.Errorf("expected ConsumerID() = 'resolved-node-id', got %q", got)
		}

		// 2. With node_id empty + BIFROST_NODE_ID set
		err = os.Setenv("BIFROST_NODE_ID", "env-node-id")
		if err != nil {
			t.Fatalf("failed to set env: %v", err)
		}

		var cfgEmpty ClusterConfig
		if got := cfgEmpty.ConsumerID(); got != "env-node-id" {
			t.Errorf("expected ConsumerID() = 'env-node-id', got %q", got)
		}

		// 3. With NodeID set to literal value
		var cfgLiteral ClusterConfig
		jsonDataLiteral := `{"node_id": "literal-node-id"}`
		if err := json.Unmarshal([]byte(jsonDataLiteral), &cfgLiteral); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if got := cfgLiteral.ConsumerID(); got != "literal-node-id" {
			t.Errorf("expected ConsumerID() = 'literal-node-id', got %q", got)
		}
	})
}
