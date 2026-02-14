package packs

import (
	"errors"
	"time"
)

// ErrMarketplaceNotFound is returned when a marketplace pack cannot be found.
var ErrMarketplaceNotFound = errors.New("marketplace pack not found")

// MarketplaceCatalogFetchTimeout is the maximum time to wait for catalog fetches.
var MarketplaceCatalogFetchTimeout = 30 * time.Second

// MarketplaceCatalogConfig is the stored catalog configuration.
type MarketplaceCatalogConfig struct {
	Catalogs []MarketplaceCatalog `json:"catalogs"`
}

// MarketplaceCatalog is a single catalog entry from configuration.
type MarketplaceCatalog struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

// MarketplaceCatalogFile is the parsed JSON from a catalog URL.
type MarketplaceCatalogFile struct {
	UpdatedAt string                   `json:"updated_at"`
	Packs     []MarketplaceCatalogPack `json:"packs"`
}

// MarketplaceCatalogPack describes a single pack in a marketplace catalog.
type MarketplaceCatalogPack struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Author       string   `json:"author"`
	Homepage     string   `json:"homepage"`
	Source       string   `json:"source"`
	Image        string   `json:"image"`
	License      string   `json:"license"`
	URL          string   `json:"url"`
	Sha256       string   `json:"sha256"`
	Capabilities []string `json:"capabilities"`
	Requires     []string `json:"requires"`
	RiskTags     []string `json:"risk_tags"`
}

// MarketplaceCatalogStatus is the runtime status of a catalog for API responses.
type MarketplaceCatalogStatus struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

// MarketplacePackItem is a single pack item in the marketplace API response.
type MarketplacePackItem struct {
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	Title            string   `json:"title,omitempty"`
	Description      string   `json:"description,omitempty"`
	Author           string   `json:"author,omitempty"`
	Homepage         string   `json:"homepage,omitempty"`
	Source           string   `json:"source,omitempty"`
	Image            string   `json:"image,omitempty"`
	License          string   `json:"license,omitempty"`
	URL              string   `json:"url,omitempty"`
	Sha256           string   `json:"sha256,omitempty"`
	CatalogID        string   `json:"catalog_id,omitempty"`
	CatalogTitle     string   `json:"catalog_title,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	Requires         []string `json:"requires,omitempty"`
	RiskTags         []string `json:"risk_tags,omitempty"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	InstalledStatus  string   `json:"installed_status,omitempty"`
	InstalledAt      string   `json:"installed_at,omitempty"`
}

// MarketplaceResponse is the API response for the marketplace listing.
type MarketplaceResponse struct {
	Catalogs  []MarketplaceCatalogStatus `json:"catalogs"`
	Items     []MarketplacePackItem      `json:"items"`
	FetchedAt string                     `json:"fetched_at,omitempty"`
	Cached    bool                       `json:"cached,omitempty"`
}

// MarketplaceCache holds a cached marketplace response.
type MarketplaceCache struct {
	Response  MarketplaceResponse
	FetchedAt time.Time
}

// CloneMarketplaceResponse returns a deep copy of the response.
func CloneMarketplaceResponse(resp MarketplaceResponse) MarketplaceResponse {
	out := resp
	if len(resp.Catalogs) > 0 {
		out.Catalogs = append([]MarketplaceCatalogStatus(nil), resp.Catalogs...)
	}
	if len(resp.Items) > 0 {
		out.Items = make([]MarketplacePackItem, len(resp.Items))
		for idx, item := range resp.Items {
			outItem := item
			if len(item.Capabilities) > 0 {
				outItem.Capabilities = append([]string(nil), item.Capabilities...)
			}
			if len(item.Requires) > 0 {
				outItem.Requires = append([]string(nil), item.Requires...)
			}
			if len(item.RiskTags) > 0 {
				outItem.RiskTags = append([]string(nil), item.RiskTags...)
			}
			out.Items[idx] = outItem
		}
	}
	return out
}

// MarketplaceCatalogEntry pairs a catalog pack with its source catalog.
type MarketplaceCatalogEntry struct {
	Pack         MarketplaceCatalogPack
	CatalogID    string
	CatalogTitle string
	CatalogURL   string
}

// MarketplaceInstallRequest is the JSON body for marketplace pack installs.
type MarketplaceInstallRequest struct {
	CatalogID string `json:"catalog_id"`
	PackID    string `json:"pack_id"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	Sha256    string `json:"sha256"`
	Force     bool   `json:"force"`
	Upgrade   bool   `json:"upgrade"`
	Inactive  bool   `json:"inactive"`
}
