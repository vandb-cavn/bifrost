package guardrails

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// ProfileClient evaluates content against an external safety provider.
type ProfileClient interface {
	Evaluate(ctx context.Context, content string) (violated bool, reason string, err error)
}

// ProviderFactory constructs a ProfileClient from a decoded ConfigJSON map.
type ProviderFactory func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error)

var (
	registryMu       sync.RWMutex
	providerRegistry = map[string]ProviderFactory{}
)

// RegisterProvider registers a factory under the given provider name.
func RegisterProvider(name string, factory ProviderFactory) {
	registryMu.Lock()
	providerRegistry[name] = factory
	registryMu.Unlock()
}

func init() {
	RegisterProvider("bedrock", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newBedrockClient(cfg, hc) })
	RegisterProvider("azure", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newAzureClient(cfg, hc) })
	RegisterProvider("grayswan", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newGraySwanClient(cfg, hc) })
	RegisterProvider("patronus_ai", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newPatronusClient(cfg, hc) })
	RegisterProvider("model_armor", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newModelArmorClient(cfg, hc) })
}

func newProfileClient(profile *configstoreTables.TableGuardrailProfile) (ProfileClient, error) {
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(profile.ConfigJSON), &cfg); err != nil {
		return nil, fmt.Errorf("invalid ConfigJSON for profile %q: %w", profile.ID, err)
	}
	registryMu.RLock()
	factory, ok := providerRegistry[profile.ProviderName]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown guardrail provider: %q (register it with RegisterProvider)", profile.ProviderName)
	}
	return factory(cfg, &http.Client{})
}

func strField(cfg map[string]interface{}, key string) (string, error) {
	v, ok := cfg[key]
	if !ok {
		return "", fmt.Errorf("missing required field %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", key)
	}
	return s, nil
}

func intFieldOr(cfg map[string]interface{}, key string, defaultVal int) int {
	v, ok := cfg[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return defaultVal
}
