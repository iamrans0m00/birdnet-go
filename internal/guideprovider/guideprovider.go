// Package guideprovider provides functionality for fetching and caching species guide text.
package guideprovider

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/sync/singleflight"
)

// GuideCacheMetrics defines the metrics interface for guide cache operations.
//
// RecordOperation/RecordDuration mirror the cross-package Recorder interface
// (see internal/observability/metrics/recorder.go) so success-path DB calls
// share the same shape as other instrumented packages. RecordDBError is the
// error-path composite that adds an error_type label.
type GuideCacheMetrics interface {
	RecordCacheHit(provider, quality string)
	RecordCacheMiss(provider string)
	RecordWikipediaAPICall(endpoint, result string, duration float64)
	RecordEBirdAPICall(endpoint, result string, duration float64)
	RecordOperation(operation, status string)
	RecordDuration(operation string, seconds float64)
	// RecordDBError records a failed DB operation: observes the duration,
	// bumps the operations counter with status="error", and bumps a dedicated
	// error counter labelled by error_type. Callers should use this instead of
	// RecordOperation(op, "error") + RecordDuration on error paths.
	RecordDBError(operation, errorType string, duration float64)
	// UpdateCachePopulationRatio records the fraction of stored cache entries that
	// contain real guide data (positive) vs not-found markers (negative).
	// This is distinct from a request-level hit ratio.
	UpdateCachePopulationRatio(positive, negative float64)
}

// Provider name constants.
const (
	WikipediaProviderName = "wikipedia" // Wikipedia REST API provider
	EBirdProviderName     = "ebird"     // eBird taxonomy enrichment provider
)

// Sentinel errors for guide operations.
var (
	// ErrGuideNotFound indicates the provider could not find guide data for the species.
	ErrGuideNotFound = errors.Newf("species guide not found").
				Component("guideprovider").
				Category(errors.CategoryNotFound).
				Context("error_type", "not_found").
				Build()

	// ErrProviderNotConfigured indicates the provider is disabled or missing credentials.
	ErrProviderNotConfigured = errors.Newf("guide provider not configured").
					Component("guideprovider").
					Category(errors.CategoryConfiguration).
					Context("error_type", "provider_not_configured").
					Build()

	// ErrAllProvidersUnavailable indicates all providers failed (circuit breakers open).
	ErrAllProvidersUnavailable = errors.Newf("all guide providers unavailable").
					Component("guideprovider").
					Category(errors.CategoryNetwork).
					Context("error_type", "all_unavailable").
					Build()

	// ErrGuideCacheNotAvailable indicates the guide cache is not initialized.
	ErrGuideCacheNotAvailable = errors.Newf("species guide not available").
					Component("guideprovider").
					Category(errors.CategoryConfiguration).
					Context("error_type", "cache_unavailable").
					Build()
)

const (
	defaultCacheTTL     = 7 * 24 * time.Hour  // 7 days for positive entries
	negativeCacheTTL    = 30 * time.Minute    // 30 minutes for negative entries
	refreshInterval     = 2 * time.Hour       // Check for stale entries every 2 hours
	refreshBatchSize    = 10                  // Number of entries to refresh in one batch
	refreshDelay        = 2 * time.Second     // Delay between refreshing individual entries
	negativeEntryMarker = "__NOT_FOUND__"     // Sentinel marker for negative cache entries
	dbRetentionPeriod   = 30 * 24 * time.Hour // 30 days: retain entries in DB beyond their TTL to ease re-fetch

	FallbackPolicyAll  = "all"  // Fallback policy to try all providers
	FallbackPolicyNone = "none" // Fallback policy to disable fallback

	providerTimeout = 10 * time.Second // Per-provider fetch timeout

	maxDescriptionLength     = 2000  // Maximum description length for summary-only fallback
	maxRichDescriptionLength = 10000 // Maximum description length for rich content with identification sections

	GuideQualityFull     = "full"      // Guide has complete identification content
	GuideQualityStub     = "stub"      // Guide has only intro paragraph
	GuideQualityNotFound = "not_found" // Negative cache entry — species has no guide data

	// DB operation status constants
	DBResultSuccess  = "success"
	DBResultNotFound = "not_found"
	DBResultError    = "error"

	DBOperationQueryGuideCaches  = "db_query:guide_caches"
	DBOperationInsertGuideCaches = "db_insert:guide_caches"
	DBOperationDeleteGuideCaches = "db_delete:guide_caches"

	// DB error_type label values for RecordDBError.
	DBErrorTypeCanceled = "context_canceled"
	DBErrorTypeDeadline = "context_deadline"
	DBErrorTypeDatabase = "database"
)

// defaultProviderName is the provider used when settings are unavailable.
const defaultProviderName = WikipediaProviderName

// defaultFallbackOrder defines providers to try in order.
var defaultFallbackOrder = []string{WikipediaProviderName, EBirdProviderName}

// getLogger returns the package logger for the guideprovider module.
func getLogger() logger.Logger {
	return logger.Global().Module("guideprovider")
}

// SpeciesGuide represents cached species guide text with metadata and attribution.
type SpeciesGuide struct {
	ScientificName     string    // Lookup key, e.g. "Turdus merula"
	CommonName         string    // e.g. "Common Blackbird"
	Description        string    // Wikipedia extract (plain text)
	ConservationStatus string    // e.g. "Least Concern" (if available)
	SimilarSpecies     []string  // Scientific names parsed from "Similar species" section
	SourceProvider     string    // Which provider supplied this data
	SourceURL          string    // Attribution link
	LicenseName        string    // e.g. "CC BY-SA 4.0"
	LicenseURL         string    // URL to the license text
	CachedAt           time.Time // When this entry was cached
	Partial            bool      // True if only some fields populated
}

// IsNegativeEntry checks if this is a negative cache entry (not found).
func (g *SpeciesGuide) IsNegativeEntry() bool {
	return g.SourceProvider == negativeEntryMarker
}

// FetchOptions holds optional parameters for guide fetching.
type FetchOptions struct {
	Locale string // Wikipedia language code (e.g. "de", "fr", "es"). Empty defaults to "en".
}

// GuideProvider defines the interface for fetching species guide text.
type GuideProvider interface {
	// Fetch retrieves guide information for a species by scientific name.
	// Returns a partial SpeciesGuide if some fields are unavailable.
	// Returns ErrGuideNotFound if the species cannot be found at all.
	Fetch(ctx context.Context, scientificName string, opts FetchOptions) (SpeciesGuide, error)
}

// GuideStore defines the datastore interface needed by the guide cache.
type GuideStore interface {
	GetGuideCache(ctx context.Context, scientificName, providerName, locale string) (*GuideCacheEntry, error)
	SaveGuideCache(ctx context.Context, entry *GuideCacheEntry) error
	GetAllGuideCaches(ctx context.Context, providerName string, notBefore time.Time) ([]GuideCacheEntry, error)
	DeleteStaleGuideCaches(ctx context.Context, providerName string, beforeTime time.Time) (int64, error)
}

// GuideCacheEntry represents a guide cache entry in the database.
type GuideCacheEntry struct {
	ID                 uint      `gorm:"primaryKey"`
	ProviderName       string    `gorm:"uniqueIndex:idx_guidecache_provider_species;size:50;not null;default:wikipedia;index:idx_guidecache_age_provider"`
	ScientificName     string    `gorm:"uniqueIndex:idx_guidecache_provider_species;not null"`
	Locale             string    `gorm:"uniqueIndex:idx_guidecache_provider_species;size:10;not null;default:en"`
	SourceProvider     string    `gorm:"size:50;not null;default:wikipedia"`
	CommonName         string    `gorm:"size:200"`
	Description        string    `gorm:"type:text"`
	ConservationStatus string    `gorm:"size:100"`
	SourceURL          string    `gorm:"size:2048"`
	LicenseName        string    `gorm:"size:200"`
	LicenseURL         string    `gorm:"size:2048"`
	SimilarSpecies     string    `gorm:"type:text"` // JSON-encoded []string
	CachedAt           time.Time `gorm:"index;index:idx_guidecache_age_provider"`
}

// TableName returns the table name for GORM.
func (GuideCacheEntry) TableName() string {
	return "guide_caches"
}

// GuideCache manages species guide data with a two-tier cache (memory + database).
type GuideCache struct {
	providers  map[string]GuideProvider
	dataMap    sync.Map
	store      GuideStore
	sfGroup    singleflight.Group
	mu         sync.RWMutex // protects providers map
	metrics    GuideCacheMetrics
	rootCtx    context.Context    // cancelled on Close(); passed to background goroutines
	rootCancel context.CancelFunc // cancels rootCtx; idempotent, so Close is safe to call twice
}

// NewGuideCache creates a new GuideCache with the given store.
func NewGuideCache(store GuideStore, metrics GuideCacheMetrics) *GuideCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &GuideCache{
		providers:  make(map[string]GuideProvider),
		store:      store,
		metrics:    metrics,
		rootCtx:    ctx,
		rootCancel: cancel,
	}
}

// RegisterProvider adds a named provider to the cache.
func (c *GuideCache) RegisterProvider(name string, provider GuideProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.providers[name] = provider
}

// Start begins the background cache refresh routine.
func (c *GuideCache) Start() {
	c.loadFromDB()
	c.startCacheRefresh()
}

// Close stops background routines. Safe to call multiple times — context.CancelFunc is idempotent.
func (c *GuideCache) Close() {
	c.rootCancel()
}

// WarmForSpecies pre-fetches guides for a list of species in the background.
// It respects rootCtx cancellation and paces requests using refreshDelay.
func (c *GuideCache) WarmForSpecies(speciesNames []string) {
	if len(speciesNames) == 0 {
		return
	}

	log := getLogger()
	log.Info("Starting guide cache warm-up",
		logger.Int("species_count", len(speciesNames)))

	go func() {
		ctx := c.rootCtx
		warmed := 0
		skipped := 0

		for _, name := range speciesNames {
			if c.shouldQuit() {
				break
			}

			// Skip if already cached in memory
			if _, ok := c.dataMap.Load(name); ok {
				skipped++
				continue
			}

			// Pace requests
			if warmed > 0 && warmed%refreshBatchSize == 0 {
				if c.waitWithQuit(refreshDelay) {
					break
				}
			}

			if _, err := c.Get(ctx, name, FetchOptions{}); err == nil {
				warmed++
			}
		}

		log.Info("Guide cache warm-up complete",
			logger.Int("warmed", warmed),
			logger.Int("skipped", skipped),
			logger.Int("total", len(speciesNames)))
	}()
}

// PreFetch triggers an async guide fetch for a species if not already cached.
// This is a non-blocking call intended for use in the detection pipeline.
func (c *GuideCache) PreFetch(ctx context.Context, scientificName string) {
	// Skip if already in memory cache
	if _, ok := c.dataMap.Load(scientificName); ok {
		return
	}

	go func() {
		prefetchCtx, cancel := context.WithTimeout(ctx, providerTimeout*2)
		defer cancel()
		_, _ = c.Get(prefetchCtx, scientificName, FetchOptions{})
	}()
}

// errCacheMiss is an internal sentinel returned by cache-tier helpers to signal
// that the entry was not present or stale in that tier. Never exposed to callers.
var errCacheMiss = errors.Newf("cache miss").Component("guideprovider").Build()

// memCacheKey returns the sync.Map key for a (scientificName, locale) pair.
// Uses the bare scientific name for the default locale ("en" or empty) so that
// entries warmed before locale support was added remain accessible.
func memCacheKey(scientificName, locale string) string {
	if locale == "" || locale == defaultLocale {
		return scientificName
	}
	return scientificName + ":" + locale
}

// Get retrieves a species guide, checking memory cache, DB cache, and providers.
// The locale parameter selects the Wikipedia language edition (e.g. "de", "fr").
// An empty locale defaults to English.
func (c *GuideCache) Get(ctx context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error) {
	providerName := c.resolveProviderName()

	// Tier 1: Memory cache
	guide, err := c.checkMemoryCache(scientificName, opts, providerName)
	if !errors.Is(err, errCacheMiss) {
		return guide, err
	}

	// Tier 2: DB cache
	guide, err = c.checkDBCache(ctx, scientificName, providerName, opts.Locale)
	if !errors.Is(err, errCacheMiss) {
		return guide, err
	}

	// Tier 3: Fetch from providers (deduplicated per locale).
	// Use rootCtx so the fetch is not cancelled if this request's context expires —
	// the result is stored in cache and benefits any concurrent callers.
	if c.metrics != nil {
		c.metrics.RecordCacheMiss(providerName)
	}
	// Use DoChan so the caller can abandon the wait when its own context is
	// cancelled. The fetch itself runs under rootCtx and continues to populate
	// the cache for other waiters even if this caller leaves.
	sfKey := memCacheKey(scientificName, opts.Locale)
	resChan := c.sfGroup.DoChan(sfKey, func() (any, error) {
		return c.fetchFromProviders(c.rootCtx, scientificName, opts)
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resChan:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*SpeciesGuide), nil
	}
}

// resolveProviderName returns the configured provider name, falling back to the default.
func (c *GuideCache) resolveProviderName() string {
	settings := conf.GetSettings()
	if settings != nil && settings.Realtime.Dashboard.SpeciesGuide.Provider != "" {
		return settings.Realtime.Dashboard.SpeciesGuide.Provider
	}
	return defaultProviderName
}

// resolveFallbackPolicy returns the configured fallback policy, falling back to FallbackPolicyAll.
func (c *GuideCache) resolveFallbackPolicy() string {
	settings := conf.GetSettings()
	if settings != nil && settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy != "" {
		return settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy
	}
	return FallbackPolicyAll
}

// checkMemoryCache checks the in-memory sync.Map tier.
// Returns errCacheMiss when the entry is absent or stale (caller should try the next tier).
// Returns ErrGuideNotFound for a fresh negative-cache hit.
// Returns nil error with a non-nil guide on a positive hit.
func (c *GuideCache) checkMemoryCache(scientificName string, opts FetchOptions, providerName string) (*SpeciesGuide, error) {
	cached, ok := c.dataMap.Load(memCacheKey(scientificName, opts.Locale))
	if !ok {
		return nil, errCacheMiss
	}
	guide := cached.(*SpeciesGuide)

	if guide.IsNegativeEntry() {
		if isCacheEntryStale(guide.CachedAt, true) {
			// Stale negative entry — fall through to re-fetch.
			return nil, errCacheMiss
		}
		// Fresh negative cache hit.
		if c.metrics != nil {
			c.metrics.RecordCacheHit(providerName, GuideQualityNotFound)
		}
		return nil, ErrGuideNotFound
	}

	if isCacheEntryStale(guide.CachedAt, false) {
		// Stale positive entry: return it immediately and refresh in background
		// (stale-while-revalidate pattern).
		c.triggerAsyncRefresh(scientificName, opts)
	}

	if c.metrics != nil {
		quality := GuideQualityFull
		if guide.Partial {
			quality = GuideQualityStub
		}
		c.metrics.RecordCacheHit(providerName, quality)
	}
	guideCopy := *guide
	return &guideCopy, nil
}

// checkDBCache checks the persistent SQLite tier.
// Returns errCacheMiss when the store is unavailable, the entry is missing, or stale.
// Returns ErrGuideNotFound for a fresh negative-cache hit.
// Returns nil error with a non-nil guide on a positive hit.
func (c *GuideCache) checkDBCache(ctx context.Context, scientificName, providerName, locale string) (*SpeciesGuide, error) {
	if c.store == nil {
		return nil, errCacheMiss
	}
	entry, err := c.store.GetGuideCache(ctx, scientificName, providerName, locale)
	if err != nil {
		// DB read error: treat as cache miss so we fall through to providers.
		return nil, errCacheMiss
	}
	if entry == nil {
		return nil, errCacheMiss
	}
	guide := dbEntryToGuide(entry)
	if isCacheEntryStale(guide.CachedAt, guide.IsNegativeEntry()) {
		return nil, errCacheMiss
	}
	c.dataMap.Store(memCacheKey(scientificName, locale), guide)
	if guide.IsNegativeEntry() {
		if c.metrics != nil {
			c.metrics.RecordCacheHit(providerName, GuideQualityNotFound)
		}
		return nil, ErrGuideNotFound
	}
	if c.metrics != nil {
		quality := GuideQualityFull
		if guide.Partial {
			quality = GuideQualityStub
		}
		c.metrics.RecordCacheHit(providerName, quality)
	}
	return guide, nil
}

// triggerAsyncRefresh starts a background goroutine to refresh stale data.
// Uses singleflight to deduplicate concurrent refreshes for the same species.
func (c *GuideCache) triggerAsyncRefresh(scientificName string, opts FetchOptions) {
	go func() {
		ctx, cancel := context.WithTimeout(c.rootCtx, 2*providerTimeout)
		defer cancel()
		sfKey := memCacheKey(scientificName, opts.Locale)
		_, _, _ = c.sfGroup.Do(sfKey, func() (any, error) {
			return c.fetchFromProviders(ctx, scientificName, opts)
		})
	}()
}

// fetchFromProviders fetches guide data from configured providers with fallback.
func (c *GuideCache) fetchFromProviders(ctx context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error) {
	log := getLogger()
	primaryProvider := c.resolveProviderName()
	fallbackPolicy := c.resolveFallbackPolicy()

	c.mu.RLock()
	provider, hasPrimary := c.providers[primaryProvider]
	c.mu.RUnlock()

	var primaryResult *SpeciesGuide

	// transientFailure tracks whether any provider returned a non-404 error
	// (timeout, circuit breaker, etc.). When true we must not negative-cache
	// the result — the species may exist but was unreachable.
	transientFailure := false

	// A configured-but-unregistered primary provider is a misconfiguration
	// (e.g. eBird selected without an API key). We must not negative-cache it,
	// and we surface a distinct error so callers can distinguish misconfig
	// from a genuine provider outage.
	primaryNotConfigured := !hasPrimary
	if primaryNotConfigured {
		log.Warn("Configured primary guide provider not registered",
			logger.String("provider", primaryProvider))
	}

	// Try primary provider
	if hasPrimary {
		providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
		guide, err := provider.Fetch(providerCtx, scientificName, opts)
		cancel()

		switch {
		case err == nil:
			primaryResult = &guide
			log.Debug("Primary provider returned guide",
				logger.String("provider", primaryProvider),
				logger.String("species", scientificName),
				logger.Bool("partial", guide.Partial))
		case errors.Is(err, ErrGuideNotFound):
			// Explicit not-found: species does not exist in this provider.
		default:
			transientFailure = true
			log.Warn("Primary provider failed",
				logger.String("provider", primaryProvider),
				logger.String("species", scientificName),
				logger.Any("error", err))
		}
	}

	// Try fallback providers if policy allows
	if fallbackPolicy == FallbackPolicyAll {
		c.mu.RLock()
		providers := make(map[string]GuideProvider, len(c.providers))
		maps.Copy(providers, c.providers) // maps.Copy requires Go 1.21+ (project minimum: Go 1.26)
		c.mu.RUnlock()

		for _, name := range defaultFallbackOrder {
			if name == primaryProvider {
				continue
			}
			fbProvider, ok := providers[name]
			if !ok {
				continue
			}

			providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
			guide, err := fbProvider.Fetch(providerCtx, scientificName, opts)
			cancel()

			if err != nil {
				if !errors.Is(err, ErrGuideNotFound) {
					transientFailure = true
				}
				continue
			}

			if primaryResult == nil {
				primaryResult = &guide
			} else {
				merged := mergeGuides(primaryResult, &guide)
				primaryResult = &merged
			}
		}
	}

	// Cache the result
	if primaryResult != nil {
		primaryResult.CachedAt = time.Now()
		c.dataMap.Store(memCacheKey(scientificName, opts.Locale), primaryResult)
		c.saveToDB(ctx, primaryResult, primaryProvider, opts.Locale)
		return primaryResult, nil
	}

	// Only negative-cache when all providers explicitly reported not-found.
	// Transient failures (timeouts, circuit breakers, etc.) and misconfiguration
	// must not be cached as 404s — the species may simply be temporarily
	// unreachable, or the provider may not be set up correctly.
	if primaryNotConfigured {
		return nil, ErrProviderNotConfigured
	}
	if transientFailure {
		return nil, ErrAllProvidersUnavailable
	}

	negative := &SpeciesGuide{
		ScientificName: scientificName,
		SourceProvider: negativeEntryMarker,
		CachedAt:       time.Now(),
	}
	c.dataMap.Store(memCacheKey(scientificName, opts.Locale), negative)
	c.saveToDB(ctx, negative, primaryProvider, opts.Locale)
	return nil, ErrGuideNotFound
}

// mergeGuides merges two guide results, with primary taking precedence.
func mergeGuides(primary, secondary *SpeciesGuide) SpeciesGuide {
	result := *primary
	if result.Description == "" {
		result.Description = secondary.Description
	}
	if result.CommonName == "" {
		result.CommonName = secondary.CommonName
	}
	if result.ConservationStatus == "" {
		result.ConservationStatus = secondary.ConservationStatus
	}
	if result.SourceURL == "" {
		result.SourceURL = secondary.SourceURL
	}
	if len(result.SimilarSpecies) == 0 {
		result.SimilarSpecies = secondary.SimilarSpecies
	}
	result.Partial = result.Description == ""
	return result
}

// TruncateUTF8 truncates s to at most maxBytes while preserving valid UTF-8.
// It never splits a multi-byte character. The suffix (e.g. "…") is appended
// only when truncation actually occurred; pass "" for none.
func TruncateUTF8(s string, maxBytes int, suffix string) string {
	if len(s) <= maxBytes {
		return s
	}
	truncated := s[:maxBytes]
	for !utf8.ValidString(truncated) && truncated != "" {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + suffix
}

// isCacheEntryStale checks if a cache entry has exceeded its TTL.
func isCacheEntryStale(cachedAt time.Time, isNegative bool) bool {
	ttl := defaultCacheTTL
	if isNegative {
		ttl = negativeCacheTTL
	}
	return time.Now().After(cachedAt.Add(ttl))
}

// dbEntryToGuide converts a database cache entry to a SpeciesGuide.
func dbEntryToGuide(entry *GuideCacheEntry) *SpeciesGuide {
	guide := &SpeciesGuide{
		ScientificName:     entry.ScientificName,
		CommonName:         entry.CommonName,
		Description:        entry.Description,
		ConservationStatus: entry.ConservationStatus,
		SourceProvider:     entry.SourceProvider,
		SourceURL:          entry.SourceURL,
		LicenseName:        entry.LicenseName,
		LicenseURL:         entry.LicenseURL,
		CachedAt:           entry.CachedAt,
		Partial:            entry.Description == "",
	}
	if entry.SimilarSpecies != "" {
		var similar []string
		if err := json.Unmarshal([]byte(entry.SimilarSpecies), &similar); err == nil {
			guide.SimilarSpecies = similar
		}
	}
	return guide
}

// encodeSimilarSpecies serializes the SimilarSpecies slice to a JSON string for
// persistence. Returns "" for nil/empty slices to keep the column compact.
func encodeSimilarSpecies(similar []string) string {
	if len(similar) == 0 {
		return ""
	}
	b, err := json.Marshal(similar)
	if err != nil {
		return ""
	}
	return string(b)
}

// saveToDB persists a guide entry to the database.
// Skips the write if the existing entry has identical content (content diffing).
func (c *GuideCache) saveToDB(ctx context.Context, guide *SpeciesGuide, providerName, locale string) {
	if c.store == nil {
		return
	}

	entry := &GuideCacheEntry{
		ProviderName:       providerName,
		ScientificName:     guide.ScientificName,
		Locale:             locale,
		SourceProvider:     guide.SourceProvider,
		CommonName:         guide.CommonName,
		Description:        TruncateUTF8(guide.Description, maxRichDescriptionLength, ""),
		ConservationStatus: guide.ConservationStatus,
		SourceURL:          guide.SourceURL,
		LicenseName:        guide.LicenseName,
		LicenseURL:         guide.LicenseURL,
		SimilarSpecies:     encodeSimilarSpecies(guide.SimilarSpecies),
		CachedAt:           guide.CachedAt,
	}

	if err := c.store.SaveGuideCache(ctx, entry); err != nil {
		getLogger().Warn("Failed to save guide cache to database",
			logger.String("species", guide.ScientificName),
			logger.Any("error", err))
	}
}

// loadFromDB loads non-stale cached entries from the database into memory.
// Entries older than their TTL are skipped to keep startup memory bounded.
func (c *GuideCache) loadFromDB() {
	if c.store == nil {
		return
	}

	providerName := c.resolveProviderName()

	// Only fetch entries within the retention period — avoids loading the entire table.
	cutoff := time.Now().Add(-dbRetentionPeriod)
	entries, err := c.store.GetAllGuideCaches(c.rootCtx, providerName, cutoff)
	if err != nil {
		getLogger().Warn("Failed to load guide caches from database",
			logger.Any("error", err))
		return
	}

	loaded, skipped := 0, 0
	for i := range entries {
		isNegative := entries[i].SourceProvider == negativeEntryMarker
		if isCacheEntryStale(entries[i].CachedAt, isNegative) {
			skipped++
			continue
		}
		guide := dbEntryToGuide(&entries[i])
		c.dataMap.Store(memCacheKey(entries[i].ScientificName, entries[i].Locale), guide)
		loaded++
	}

	getLogger().Info("Loaded guide cache entries from database",
		logger.Int("loaded", loaded),
		logger.Int("skipped_stale", skipped))
}

// startCacheRefresh starts the background cache refresh routine.
func (c *GuideCache) startCacheRefresh() {
	log := getLogger()
	log.Info("Starting guide cache refresh routine",
		logger.Duration("ttl", defaultCacheTTL),
		logger.Duration("interval", refreshInterval))

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.rootCtx.Done():
				log.Info("Stopping guide cache refresh routine")
				return
			case <-ticker.C:
				c.refreshStaleEntries()
			}
		}
	}()
}

// refreshStaleEntries refreshes cache entries that have exceeded their TTL.
func (c *GuideCache) refreshStaleEntries() {
	if c.store == nil {
		return
	}

	log := getLogger()
	providerName := c.resolveProviderName()

	// Only fetch entries within retention period — no point refreshing entries
	// that will be deleted by the next cleanup cycle.
	cutoff := time.Now().Add(-dbRetentionPeriod)
	entries, err := c.store.GetAllGuideCaches(c.rootCtx, providerName, cutoff)
	if err != nil {
		log.Warn("Failed to get guide caches for refresh", logger.Any("error", err))
		return
	}

	type staleKey struct {
		Name   string
		Locale string
	}
	staleEntries := make([]staleKey, 0, len(entries))
	for i := range entries {
		isNegative := entries[i].SourceProvider == negativeEntryMarker
		if isCacheEntryStale(entries[i].CachedAt, isNegative) {
			staleEntries = append(staleEntries, staleKey{
				Name:   entries[i].ScientificName,
				Locale: entries[i].Locale,
			})
		}
	}

	if len(staleEntries) == 0 {
		return
	}

	log.Info("Found stale guide cache entries to refresh",
		logger.Int("count", len(staleEntries)))

	ctx := c.rootCtx
	refreshed := 0
	for i, key := range staleEntries {
		if c.shouldQuit() {
			break
		}
		if i > 0 && i%refreshBatchSize == 0 {
			if c.waitWithQuit(refreshDelay) {
				break
			}
		}

		if _, err := c.fetchFromProviders(ctx, key.Name, FetchOptions{Locale: key.Locale}); err == nil {
			refreshed++
		}
	}

	log.Info("Finished refreshing stale guide entries",
		logger.Int("refreshed", refreshed),
		logger.Int("total", len(staleEntries)))

	// Update cache population ratio metric — reuse the already-fetched entries slice.
	c.updateCachePopulationRatio(entries)

	// Clean up very old entries from database (beyond retention period).
	// This prevents unbounded database growth while keeping recent entries for quick re-fetch.
	// Reuse the cutoff computed above for consistency and to avoid a second time.Now().
	deleted, err := c.store.DeleteStaleGuideCaches(c.rootCtx, providerName, cutoff)
	if err != nil {
		log.Warn("Failed to clean up old guide cache entries",
			logger.String("provider", providerName),
			logger.Any("error", err))
	} else if deleted > 0 {
		log.Debug("Cleaned up old guide cache entries",
			logger.Int64("deleted", deleted),
			logger.String("provider", providerName))
	}
}

// shouldQuit reports whether rootCtx has been cancelled (Close was called).
func (c *GuideCache) shouldQuit() bool {
	return c.rootCtx.Err() != nil
}

// waitWithQuit waits for the specified duration, returning true if rootCtx was cancelled.
func (c *GuideCache) waitWithQuit(d time.Duration) bool {
	timer := time.NewTimer(d)
	select {
	case <-c.rootCtx.Done():
		timer.Stop()
		return true
	case <-timer.C:
		return false
	}
}

// updateCachePopulationRatio calculates and updates the cache population ratio metric
// from a pre-fetched slice, avoiding a redundant database query. It reports what
// fraction of stored entries contain real guide data versus not-found markers.
func (c *GuideCache) updateCachePopulationRatio(entries []GuideCacheEntry) {
	if c.metrics == nil {
		return
	}

	var positive, negative int
	for i := range entries {
		if entries[i].SourceProvider == negativeEntryMarker {
			negative++
		} else {
			positive++
		}
	}

	c.metrics.UpdateCachePopulationRatio(float64(positive), float64(negative))
}
