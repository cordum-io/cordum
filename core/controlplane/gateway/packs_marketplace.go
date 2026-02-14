package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/redis/go-redis/v9"
)

var errMarketplaceNotFound = errors.New("marketplace pack not found")
var marketplaceCatalogFetchTimeout = 30 * time.Second

func seedDefaultPackCatalogs(ctx context.Context, svc *configsvc.Service) error {
	if svc == nil {
		return nil
	}
	disabled := strings.TrimSpace(os.Getenv(envPackCatalogDisableDefault))
	if disabled != "" {
		switch strings.ToLower(disabled) {
		case "1", "true", "yes":
			return nil
		}
	}
	catalogURL := strings.TrimSpace(os.Getenv(envPackCatalogURL))
	if catalogURL == "" {
		catalogURL = defaultPackCatalogURL
	}
	if catalogURL == "" {
		return nil
	}
	title := strings.TrimSpace(os.Getenv(envPackCatalogTitle))
	if title == "" {
		title = defaultPackCatalogTitle
	}
	catalogID := strings.TrimSpace(os.Getenv(envPackCatalogID))
	if catalogID == "" {
		catalogID = defaultPackCatalogID
	}

	doc, err := svc.Get(ctx, configsvc.Scope(packCatalogScope), packCatalogID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return err
		}
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(packCatalogScope),
			ScopeID: packCatalogID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	if existing, ok := doc.Data["catalogs"]; ok && existing != nil {
		switch typed := existing.(type) {
		case []any:
			if len(typed) > 0 {
				return nil
			}
		case []map[string]any:
			if len(typed) > 0 {
				return nil
			}
		default:
			return nil
		}
	}

	doc.Data["catalogs"] = []map[string]any{
		{
			"id":      catalogID,
			"title":   title,
			"url":     catalogURL,
			"enabled": true,
		},
	}
	return svc.Set(ctx, doc)
}

type marketplaceCatalogConfig struct {
	Catalogs []marketplaceCatalog `json:"catalogs"`
}

type marketplaceCatalog struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

type marketplaceCatalogFile struct {
	UpdatedAt string                   `json:"updated_at"`
	Packs     []marketplaceCatalogPack `json:"packs"`
}

type marketplaceCatalogPack struct {
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

type marketplaceCatalogStatus struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

type marketplacePackItem struct {
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

type marketplaceResponse struct {
	Catalogs  []marketplaceCatalogStatus `json:"catalogs"`
	Items     []marketplacePackItem      `json:"items"`
	FetchedAt string                     `json:"fetched_at,omitempty"`
	Cached    bool                       `json:"cached,omitempty"`
}

type marketplaceCache struct {
	Response  marketplaceResponse
	FetchedAt time.Time
}

func cloneMarketplaceResponse(resp marketplaceResponse) marketplaceResponse {
	out := resp
	if len(resp.Catalogs) > 0 {
		out.Catalogs = append([]marketplaceCatalogStatus(nil), resp.Catalogs...)
	}
	if len(resp.Items) > 0 {
		out.Items = make([]marketplacePackItem, len(resp.Items))
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

type marketplaceCatalogEntry struct {
	Pack         marketplaceCatalogPack
	CatalogID    string
	CatalogTitle string
	CatalogURL   string
}

type marketplaceInstallRequest struct {
	CatalogID string `json:"catalog_id"`
	PackID    string `json:"pack_id"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	Sha256    string `json:"sha256"`
	Force     bool   `json:"force"`
	Upgrade   bool   `json:"upgrade"`
	Inactive  bool   `json:"inactive"`
}

func (s *server) handleMarketplacePacks(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "marketplace operation failed")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	resp, err := s.marketplaceSnapshot(r.Context(), false)
	if err != nil {
		slog.Error("marketplace snapshot failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "marketplace operation failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleMarketplaceInstall(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil || s.schemaRegistry == nil || s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "marketplace operation failed")
		return
	}
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "marketplace operation failed")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req marketplaceInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	allowedHosts, err := s.marketplaceAllowedHosts(r.Context())
	if err != nil {
		slog.Error("marketplace allowed hosts lookup failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "marketplace operation failed")
		return
	}
	installURL := strings.TrimSpace(req.URL)
	expectedSha := strings.TrimSpace(req.Sha256)
	fromCatalog := false
	if installURL != "" {
		if expectedSha == "" {
			writeErrorJSON(w, http.StatusBadRequest, "sha256 required")
			return
		}
		entry, err := s.findMarketplaceEntryByURL(r.Context(), installURL)
		if err != nil {
			if errors.Is(err, errMarketplaceNotFound) {
				writeErrorJSON(w, http.StatusNotFound, "marketplace pack not found")
			} else {
				slog.Error("marketplace entry lookup failed", "error", err, "url", installURL)
				writeErrorJSON(w, http.StatusBadRequest, "marketplace lookup failed")
			}
			return
		}
		entryURL := strings.TrimSpace(entry.Pack.URL)
		entrySha := strings.TrimSpace(entry.Pack.Sha256)
		if entryURL == "" || entrySha == "" {
			writeErrorJSON(w, http.StatusBadRequest, "marketplace entry missing url or sha256")
			return
		}
		if !strings.EqualFold(expectedSha, entrySha) {
			writeErrorJSON(w, http.StatusBadRequest, "sha256 mismatch")
			return
		}
		installURL = resolvePackURL(entryURL, entry.CatalogURL)
		expectedSha = entrySha
		fromCatalog = true
	} else {
		catalogID := strings.TrimSpace(req.CatalogID)
		packID := strings.TrimSpace(req.PackID)
		if catalogID == "" || packID == "" {
			writeErrorJSON(w, http.StatusBadRequest, "catalog_id and pack_id required")
			return
		}
		entry, err := s.findMarketplaceEntry(r.Context(), catalogID, packID, strings.TrimSpace(req.Version))
		if err != nil {
			if errors.Is(err, errMarketplaceNotFound) {
				writeErrorJSON(w, http.StatusNotFound, "marketplace pack not found")
			} else {
				slog.Error("marketplace entry lookup failed", "error", err, "catalog_id", catalogID, "pack_id", packID)
				writeErrorJSON(w, http.StatusBadRequest, "marketplace lookup failed")
			}
			return
		}
		installURL = resolvePackURL(strings.TrimSpace(entry.Pack.URL), entry.CatalogURL)
		expectedSha = strings.TrimSpace(entry.Pack.Sha256)
		fromCatalog = true
	}
	if installURL == "" {
		writeErrorJSON(w, http.StatusBadRequest, "download url required")
		return
	}
	if expectedSha == "" {
		writeErrorJSON(w, http.StatusBadRequest, "sha256 required")
		return
	}
	if fromCatalog {
		if _, err := validateMarketplaceURL(installURL, nil); err != nil {
			slog.Error("marketplace url validation failed", "error", err, "url", installURL) // #nosec -- URL is validated and used for diagnostics.
			writeErrorJSON(w, http.StatusBadRequest, "invalid pack url")
			return
		}
		if host := hostFromURL(installURL); host != "" {
			allowedHosts[host] = struct{}{}
		}
	}
	parsed, err := validateMarketplaceURL(installURL, allowedHosts)
	if err != nil {
		slog.Error("marketplace url validation failed", "error", err, "url", installURL) // #nosec -- URL is validated and used for diagnostics.
		writeErrorJSON(w, http.StatusBadRequest, "invalid pack url")
		return
	}
	packFile, digest, cleanup, err := downloadPackBundle(r.Context(), parsed, allowedHosts)
	if err != nil {
		slog.Error("pack download failed", "error", err)
		writeErrorJSON(w, http.StatusBadRequest, "pack download failed")
		return
	}
	defer cleanup()
	if !strings.EqualFold(digest, expectedSha) {
		writeErrorJSON(w, http.StatusBadRequest, "sha256 mismatch")
		return
	}
	// #nosec G304 -- packFile is a temp file path created by this process.
	fp, err := os.Open(packFile)
	if err != nil {
		slog.Error("pack file open failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "pack processing failed")
		return
	}
	bundleDir, cleanupDir, err := loadPackBundleFromReader(fp)
	_ = fp.Close()
	if err != nil {
		slog.Error("pack bundle load failed", "error", err)
		writeErrorJSON(w, http.StatusBadRequest, "invalid pack bundle")
		return
	}
	defer cleanupDir()

	record, err := s.installPackFromDir(r.Context(), bundleDir, packInstallOptions{
		Force:       req.Force,
		Upgrade:     req.Upgrade,
		Inactive:    req.Inactive,
		Owner:       packLockOwner(r),
		InstalledBy: strings.TrimSpace(policyActorID(r)),
	})
	if err != nil {
		var installErr *packInstallError
		if errors.As(err, &installErr) {
			writeErrorJSON(w, installErr.Status, installErr.Error())
		} else {
			slog.Error("pack install failed", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "pack installation failed")
		}
		return
	}

	s.appendAuditEntryNamed(r.Context(), "install", "pack", record.ID, record.Manifest.Metadata.Title, policyActorID(r), policyRole(r), "install marketplace pack "+record.ID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, record)
}

func (s *server) marketplaceSnapshot(ctx context.Context, refresh bool) (marketplaceResponse, error) {
	if s == nil {
		return marketplaceResponse{}, errors.New("marketplace unavailable")
	}
	if !refresh {
		s.marketplaceMu.Lock()
		cache := s.marketplaceCache
		if !cache.FetchedAt.IsZero() && time.Since(cache.FetchedAt) < marketplaceCacheTTL {
			resp := cloneMarketplaceResponse(cache.Response)
			resp.Cached = true
			if resp.FetchedAt == "" {
				resp.FetchedAt = cache.FetchedAt.UTC().Format(time.RFC3339)
			}
			s.marketplaceMu.Unlock()
			return resp, nil
		}
		s.marketplaceMu.Unlock()
	}
	catalogs, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return marketplaceResponse{}, err
	}
	resp, err := s.buildMarketplaceResponse(ctx, catalogs, entries)
	if err != nil {
		return marketplaceResponse{}, err
	}
	fetchedAt := time.Now().UTC()
	resp.FetchedAt = fetchedAt.Format(time.RFC3339)
	s.marketplaceMu.Lock()
	s.marketplaceCache = marketplaceCache{Response: resp, FetchedAt: fetchedAt}
	s.marketplaceMu.Unlock()
	return resp, nil
}

func (s *server) loadMarketplaceEntries(ctx context.Context) ([]marketplaceCatalogStatus, []marketplaceCatalogEntry, error) {
	catalogs, err := s.loadPackCatalogs(ctx)
	if err != nil {
		return nil, nil, err
	}
	statuses := make([]marketplaceCatalogStatus, 0, len(catalogs))
	entries := []marketplaceCatalogEntry{}
	for idx, catalog := range catalogs {
		id := strings.TrimSpace(catalog.ID)
		if id == "" {
			id = fmt.Sprintf("catalog-%d", idx+1)
		}
		enabled := true
		if catalog.Enabled != nil {
			enabled = *catalog.Enabled
		}
		status := marketplaceCatalogStatus{
			ID:      id,
			Title:   strings.TrimSpace(catalog.Title),
			URL:     strings.TrimSpace(catalog.URL),
			Enabled: enabled,
		}
		if !enabled {
			statuses = append(statuses, status)
			continue
		}
		allowedHosts := map[string]struct{}{}
		if host := hostFromURL(status.URL); host != "" {
			allowedHosts[host] = struct{}{}
		}
		fetchCtx, cancel := context.WithTimeout(ctx, marketplaceCatalogFetchTimeout)
		catalogFile, err := fetchMarketplaceCatalog(fetchCtx, status.URL, allowedHosts)
		cancel()
		if err != nil {
			slog.Error("marketplace catalog fetch failed", "catalog_id", id, "url", status.URL, "error", err)
			status.Error = "catalog fetch failed"
			statuses = append(statuses, status)
			continue
		}
		status.UpdatedAt = catalogFile.UpdatedAt
		statuses = append(statuses, status)
		for _, pack := range catalogFile.Packs {
			entries = append(entries, marketplaceCatalogEntry{
				Pack:         pack,
				CatalogID:    id,
				CatalogTitle: status.Title,
				CatalogURL:   status.URL,
			})
		}
	}
	return statuses, entries, nil
}

func (s *server) loadPackCatalogs(ctx context.Context) ([]marketplaceCatalog, error) {
	if s.configSvc == nil {
		return nil, errors.New("marketplace configuration unavailable")
	}
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(packCatalogScope), packCatalogID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if doc == nil || doc.Data == nil {
		return nil, nil
	}
	payload, err := json.Marshal(normalizeJSON(doc.Data))
	if err != nil {
		return nil, err
	}
	var cfg marketplaceCatalogConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return nil, err
	}
	return cfg.Catalogs, nil
}

func fetchMarketplaceCatalog(ctx context.Context, catalogURL string, allowedHosts map[string]struct{}) (*marketplaceCatalogFile, error) {
	parsed, err := validateMarketplaceURL(catalogURL, allowedHosts)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	client := marketplaceHTTPClient(allowedHosts, parsed.Hostname())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("catalog fetch failed: %s", resp.Status)
	}
	limit := int64(maxCatalogBytes) + 1
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > int64(maxCatalogBytes) {
		return nil, fmt.Errorf("catalog exceeds max size (%d bytes)", maxCatalogBytes)
	}
	var out marketplaceCatalogFile
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *server) buildMarketplaceResponse(ctx context.Context, catalogs []marketplaceCatalogStatus, entries []marketplaceCatalogEntry) (marketplaceResponse, error) {
	records := map[string]packRecord{}
	if s.configSvc != nil {
		var err error
		records, _, err = s.loadPackRegistry(ctx)
		if err != nil {
			return marketplaceResponse{}, err
		}
	}
	latest := map[string]marketplaceCatalogEntry{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.Pack.ID)
		version := strings.TrimSpace(entry.Pack.Version)
		url := strings.TrimSpace(entry.Pack.URL)
		sha := strings.TrimSpace(entry.Pack.Sha256)
		if id == "" || version == "" || url == "" || sha == "" {
			continue
		}
		if existing, ok := latest[id]; ok {
			if compareVersions(version, existing.Pack.Version) <= 0 {
				continue
			}
		}
		latest[id] = entry
	}
	items := make([]marketplacePackItem, 0, len(latest))
	for _, entry := range latest {
		pack := entry.Pack
		item := marketplacePackItem{
			ID:           pack.ID,
			Version:      pack.Version,
			Title:        pack.Title,
			Description:  pack.Description,
			Author:       pack.Author,
			Homepage:     pack.Homepage,
			Source:       pack.Source,
			Image:        pack.Image,
			License:      pack.License,
			URL:          pack.URL,
			Sha256:       pack.Sha256,
			CatalogID:    entry.CatalogID,
			CatalogTitle: entry.CatalogTitle,
			Capabilities: pack.Capabilities,
			Requires:     pack.Requires,
			RiskTags:     pack.RiskTags,
		}
		if rec, ok := records[pack.ID]; ok {
			item.InstalledVersion = rec.Version
			item.InstalledStatus = rec.Status
			item.InstalledAt = rec.InstalledAt
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return marketplaceResponse{
		Catalogs: catalogs,
		Items:    items,
	}, nil
}

func (s *server) findMarketplaceEntry(ctx context.Context, catalogID, packID, version string) (marketplaceCatalogEntry, error) {
	catalogID = strings.TrimSpace(catalogID)
	packID = strings.TrimSpace(packID)
	version = strings.TrimSpace(version)
	if packID == "" {
		return marketplaceCatalogEntry{}, errMarketplaceNotFound
	}
	_, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return marketplaceCatalogEntry{}, err
	}
	var best marketplaceCatalogEntry
	found := false
	for _, entry := range entries {
		if catalogID != "" && entry.CatalogID != catalogID {
			continue
		}
		if strings.TrimSpace(entry.Pack.ID) != packID {
			continue
		}
		if strings.TrimSpace(entry.Pack.URL) == "" || strings.TrimSpace(entry.Pack.Sha256) == "" {
			continue
		}
		if version != "" {
			if strings.TrimSpace(entry.Pack.Version) != version {
				continue
			}
			return entry, nil
		}
		if !found || compareVersions(entry.Pack.Version, best.Pack.Version) > 0 {
			best = entry
			found = true
		}
	}
	if !found {
		return marketplaceCatalogEntry{}, errMarketplaceNotFound
	}
	return best, nil
}

func (s *server) findMarketplaceEntryByURL(ctx context.Context, rawURL string) (marketplaceCatalogEntry, error) {
	urlTrim := strings.TrimSpace(rawURL)
	if urlTrim == "" {
		return marketplaceCatalogEntry{}, errMarketplaceNotFound
	}
	_, entries, err := s.loadMarketplaceEntries(ctx)
	if err != nil {
		return marketplaceCatalogEntry{}, err
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Pack.URL) == urlTrim {
			return entry, nil
		}
	}
	return marketplaceCatalogEntry{}, errMarketplaceNotFound
}

func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if oka && okb {
		max := len(pa)
		if len(pb) > max {
			max = len(pb)
		}
		for i := 0; i < max; i++ {
			ai := 0
			bi := 0
			if i < len(pa) {
				ai = pa[i]
			}
			if i < len(pb) {
				bi = pb[i]
			}
			if ai > bi {
				return 1
			}
			if ai < bi {
				return -1
			}
		}
		return 0
	}
	na := normalizeVersion(a)
	nb := normalizeVersion(b)
	if na == nb {
		return 0
	}
	if na > nb {
		return 1
	}
	return -1
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	return version
}

func parseVersion(version string) ([]int, bool) {
	version = normalizeVersion(version)
	if version == "" {
		return nil, false
	}
	if strings.ContainsAny(version, "+-") {
		return nil, false
	}
	parts := strings.Split(version, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func downloadPackBundle(ctx context.Context, parsed *url.URL, allowedHosts map[string]struct{}) (string, string, func(), error) {
	if parsed == nil {
		return "", "", func() {}, errors.New("url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", "", func() {}, err
	}
	client := marketplaceHTTPClient(allowedHosts, "")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", "", func() {}, fmt.Errorf("download failed: %s", resp.Status)
	}
	tmpFile, err := os.CreateTemp("", "cordum-pack-*.tgz")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.Remove(tmpFile.Name()) }
	hasher := sha256.New()
	limit := int64(maxPackUploadBytes) + 1
	limited := &io.LimitedReader{R: resp.Body, N: limit}
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), limited)
	if err != nil {
		_ = tmpFile.Close()
		cleanup()
		return "", "", func() {}, err
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	if written > int64(maxPackUploadBytes) {
		cleanup()
		return "", "", func() {}, fmt.Errorf("pack download exceeds max size (%d bytes)", maxPackUploadBytes)
	}
	return tmpFile.Name(), hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}

// resolvePackURL resolves a potentially relative pack URL against its catalog base URL.
func resolvePackURL(packURL, catalogURL string) string {
	packURL = strings.TrimSpace(packURL)
	if packURL == "" {
		return packURL
	}
	parsed, err := url.Parse(packURL)
	if err != nil || parsed.Scheme != "" {
		return packURL // already absolute or unparseable
	}
	base, err := url.Parse(strings.TrimSpace(catalogURL))
	if err != nil || base.Scheme == "" {
		return packURL
	}
	return base.ResolveReference(parsed).String()
}

// privateIPNets are RFC 1918 / RFC 4193 / link-local / loopback ranges.
var privateIPNets = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local / AWS metadata
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique-local (RFC 4193)
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("bad private CIDR: " + cidr)
		}
		nets = append(nets, n)
	}
	return nets
}()

// privateHostnames are hostnames that always resolve to private/internal addresses.
var privateHostnames = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
}

// skipPrivateIPCheck disables SSRF protection. Only set in tests.
var skipPrivateIPCheck atomic.Bool

// lookupHostIPs resolves hostnames for SSRF checks. Overridden in tests.
var lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP != nil {
			ips = append(ips, addr.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("no resolved IPs")
	}
	return ips, nil
}

// isPrivateIP returns true if host is a private/loopback/link-local IP address
// or a well-known hostname that resolves to one.
func isPrivateIP(host string) bool {
	if skipPrivateIPCheck.Load() {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if privateHostnames[host] {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateNet(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), marketplaceHTTPTimeout())
	defer cancel()
	ips, err := lookupHostIPs(ctx, host)
	if err != nil {
		return true
	}
	for _, ip := range ips {
		if isPrivateNet(ip) {
			return true
		}
	}
	return false
}

func isPrivateNet(ip net.IP) bool {
	if ip == nil {
		return true
	}
	for _, n := range privateIPNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func resolveMarketplaceIPs(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, errors.New("host required")
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return lookupHostIPs(ctx, host)
}

func validateMarketplaceHost(ctx context.Context, host string, allowedHosts map[string]struct{}) ([]net.IP, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, errors.New("url host required")
	}
	if allowedHosts != nil {
		if len(allowedHosts) == 0 {
			return nil, errors.New("invalid pack url")
		}
		if _, ok := allowedHosts[host]; !ok {
			slog.Warn("marketplace URL blocked: host not in allowlist", "host", host) // #nosec -- host is validated and used for diagnostics.
			return nil, errors.New("invalid pack url")
		}
	}
	if skipPrivateIPCheck.Load() {
		return resolveMarketplaceIPs(ctx, host)
	}
	if privateHostnames[host] {
		slog.Warn("marketplace URL blocked: private address", "host", host) // #nosec -- host is validated and used for diagnostics.
		return nil, errors.New("invalid pack url")
	}
	ips, err := resolveMarketplaceIPs(ctx, host)
	if err != nil {
		slog.Warn("marketplace URL blocked: host resolution failed", "host", host, "error", err) // #nosec -- host is validated and used for diagnostics.
		return nil, errors.New("invalid pack url")
	}
	for _, ip := range ips {
		if isPrivateNet(ip) {
			slog.Warn("marketplace URL blocked: private address", "host", host, "ip", ip.String()) // #nosec -- host is validated and used for diagnostics.
			return nil, errors.New("invalid pack url")
		}
	}
	return ips, nil
}

func validateMarketplaceURL(rawURL string, allowedHosts map[string]struct{}) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, errors.New("url required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "https":
		// ok
	case "http":
		if env.IsProduction() && !env.Bool(envMarketplaceAllowHTTP) {
			return nil, fmt.Errorf("http scheme not allowed")
		}
	default:
		return nil, fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return nil, errors.New("url host required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), marketplaceHTTPTimeout())
	defer cancel()
	if _, err := validateMarketplaceHost(ctx, host, allowedHosts); err != nil {
		return nil, err
	}
	return parsed, nil
}

func marketplaceHTTPTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(envMarketplaceHTTPTimeout)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultMarketplaceHTTPTimeout
}

func marketplaceHTTPClient(allowedHosts map[string]struct{}, initialHost string) *http.Client {
	initialHost = strings.ToLower(strings.TrimSpace(initialHost))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = marketplaceDialContext(allowedHosts)
	return &http.Client{
		Timeout:   marketplaceHTTPTimeout(),
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			redirectHost := strings.ToLower(req.URL.Hostname())
			if initialHost != "" && redirectHost != "" && redirectHost != initialHost {
				if allowedHosts == nil {
					return errors.New("redirect not allowed")
				}
				if _, ok := allowedHosts[redirectHost]; !ok {
					return errors.New("redirect not allowed")
				}
			}
			if _, err := validateMarketplaceURL(req.URL.String(), allowedHosts); err != nil {
				return err
			}
			return nil
		},
	}
}

func marketplaceDialContext(allowedHosts map[string]struct{}) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		resolveCtx, cancel := context.WithTimeout(ctx, marketplaceHTTPTimeout())
		ips, err := validateMarketplaceHost(resolveCtx, host, allowedHosts)
		cancel()
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("no resolved IPs")
	}
}

func hostFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	return host
}

func (s *server) marketplaceAllowedHosts(ctx context.Context) (map[string]struct{}, error) {
	hosts := map[string]struct{}{}
	catalogs, err := s.loadPackCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	if len(catalogs) == 0 {
		disabled := strings.TrimSpace(os.Getenv(envPackCatalogDisableDefault))
		if disabled != "" {
			switch strings.ToLower(disabled) {
			case "1", "true", "yes":
				return hosts, nil
			}
		}
		catalogURL := strings.TrimSpace(os.Getenv(envPackCatalogURL))
		if catalogURL == "" {
			catalogURL = defaultPackCatalogURL
		}
		if host := hostFromURL(catalogURL); host != "" {
			if isPrivateIP(host) {
				slog.WarnContext(ctx, "skipping default catalog with private IP", "host", host)
			} else {
				hosts[host] = struct{}{}
			}
		}
		return hosts, nil
	}
	for _, catalog := range catalogs {
		enabled := true
		if catalog.Enabled != nil {
			enabled = *catalog.Enabled
		}
		if !enabled {
			continue
		}
		if host := hostFromURL(catalog.URL); host != "" {
			if isPrivateIP(host) {
				slog.WarnContext(ctx, "skipping catalog with private IP", "host", host, "url", catalog.URL)
				continue
			}
			hosts[host] = struct{}{}
		}
	}
	return hosts, nil
}
