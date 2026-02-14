package packs

import "time"

const (
	PackRegistryScope = "system"
	PackRegistryID    = "packs"

	PackCatalogScope        = "system"
	PackCatalogID           = "pack_catalogs"
	DefaultPackCatalogID    = "official"
	DefaultPackCatalogTitle = "Cordum Official"
	DefaultPackCatalogURL   = "https://packs.cordum.io/catalog.json"

	EnvPackCatalogID             = "CORDUM_PACK_CATALOG_ID"
	EnvPackCatalogTitle          = "CORDUM_PACK_CATALOG_TITLE"
	EnvPackCatalogURL            = "CORDUM_PACK_CATALOG_URL"
	EnvPackCatalogDisableDefault = "CORDUM_PACK_CATALOG_DEFAULT_DISABLED"
	EnvMarketplaceAllowHTTP      = "CORDUM_MARKETPLACE_ALLOW_HTTP"
	EnvMarketplaceHTTPTimeout    = "CORDUM_MARKETPLACE_HTTP_TIMEOUT"

	PolicyConfigScope = "system"
	PolicyConfigID    = "policy"
	PolicyConfigKey   = "bundles"

	MaxPackUploadBytes       = 64 << 20
	MaxPackFiles             = 2048
	MaxPackFileBytes         = 32 << 20
	MaxPackUncompressedBytes = 256 << 20
	MaxCatalogBytes          = 8 << 20

	MarketplaceCacheTTL           = 30 * time.Second
	DefaultMarketplaceHTTPTimeout = 15 * time.Second
)
