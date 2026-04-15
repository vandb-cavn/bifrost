package modelcatalog

import (
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// NewMinimalCatalogForHandlerTests returns a ModelCatalog that supports only
// in-memory pricing override operations (UpsertPricingOverrides / DeletePricingOverride).
// Intended for transport-layer unit tests; do not use for full pricing or sync behavior.
func NewMinimalCatalogForHandlerTests() *ModelCatalog {
	return &ModelCatalog{
		modelPool:           make(map[schemas.ModelProvider][]string),
		unfilteredModelPool: make(map[schemas.ModelProvider][]string),
		baseModelIndex:      make(map[string]string),
		pricingData:         make(map[string]configstoreTables.TableModelPricing),
	}
}
