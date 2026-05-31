package modelcatalog

import "testing"

// Guards against the port regression where loadModelParametersFromURL hardcoded
// DefaultModelParametersURL instead of the configurable mc.modelParametersURL.
func TestModelParametersURL_IsConfigurable(t *testing.T) {
	mc := &ModelCatalog{}
	const custom = "https://example.test/custom-model-params"
	mc.modelParametersURL = custom
	if got := mc.getModelParametersURL(); got != custom {
		t.Fatalf("getModelParametersURL() = %q, want %q", got, custom)
	}
}
