package lib

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/redis/go-redis/v9"
)

// ClusterTLSConfig holds TLS settings for the cluster Redis connection.
type ClusterTLSConfig struct {
	Enabled  bool   `json:"enabled"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	CAFile   string `json:"ca_file"`
}

// ClusterRedisConfig holds connection parameters for the cluster Redis.
type ClusterRedisConfig struct {
	Addr        string           `json:"addr"`
	Addrs       []string         `json:"addrs"`
	ClusterMode bool             `json:"cluster_mode"`
	Password    string           `json:"password"`
	DB          int              `json:"db"`
	PoolSize    int              `json:"pool_size"`
	TLS         ClusterTLSConfig `json:"tls"`
}

// ClusterConfig is the top-level optional cluster block in config.json.
// Absent → single-node mode, all Redis code paths skipped.
type ClusterConfig struct {
	NodeID        string             `json:"node_id"`
	StrictBudgets bool               `json:"strict_budgets"`
	Redis         ClusterRedisConfig `json:"redis"`
}

// ConsumerID returns the stable consumer identity for cursor persistence.
// Prefers cluster.node_id, then BIFROST_NODE_ID env var, then os.Hostname().
func (c *ClusterConfig) ConsumerID() string {
	if c != nil && c.NodeID != "" {
		return c.NodeID
	}
	if env := os.Getenv("BIFROST_NODE_ID"); env != "" {
		return env
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "bifrost-node"
}

// StrictBudgetsEnabled returns whether strict DB budget checks are requested.
func (c *ClusterConfig) StrictBudgetsEnabled() bool {
	return c != nil && c.StrictBudgets
}

func (r *ClusterRedisConfig) poolSizeOrDefault() int {
	if r.PoolSize <= 0 {
		return 20
	}
	return r.PoolSize
}

func (r *ClusterRedisConfig) tlsConfig() (*tls.Config, error) {
	if !r.TLS.Enabled {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if r.TLS.CAFile != "" {
		pemData, err := os.ReadFile(r.TLS.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("parse CA PEM from %s", r.TLS.CAFile)
		}
		cfg.RootCAs = pool
	}
	if r.TLS.CertFile != "" && r.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(r.TLS.CertFile, r.TLS.KeyFile)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// NewRedisUniversalClient builds a redis.UniversalClient from ClusterRedisConfig.
// Returns nil if addr and addrs are both empty (no Redis configured).
func (r *ClusterRedisConfig) NewRedisUniversalClient() (redis.UniversalClient, error) {
	addrs := r.Addrs
	if len(addrs) == 0 && r.Addr != "" {
		addrs = []string{r.Addr}
	}
	if len(addrs) == 0 {
		return nil, nil
	}
	tlsCfg, err := r.tlsConfig()
	if err != nil {
		return nil, err
	}
	ps := r.poolSizeOrDefault()
	if r.ClusterMode {
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:      addrs,
			Password:   r.Password,
			PoolSize:   ps,
			TLSConfig:  tlsCfg,
			MaxRetries: 3,
		}), nil
	}
	return redis.NewClient(&redis.Options{
		Addr:       addrs[0],
		Password:   r.Password,
		DB:         r.DB,
		PoolSize:   ps,
		TLSConfig:  tlsCfg,
		MaxRetries: 3,
	}), nil
}
