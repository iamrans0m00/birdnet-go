package guideprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/time/rate"
)

const (
	// Wikipedia API endpoint templates. Use wikipediaURLs() to get locale-specific URLs.
	wikipediaRESTTemplate   = "https://%s.wikipedia.org/api/rest_v1/page/summary"
	wikipediaActionTemplate = "https://%s.wikipedia.org/w/api.php"

	// Default locale for Wikipedia API requests.
	defaultLocale = "en"

	// sectionNameCanto is the localized section heading used in Spanish, Portuguese, and Italian.
	sectionNameCanto = "Canto"

	// English Wikipedia section heading constants used across identification and song slices.
	sectionDescription   = "Description"
	sectionSongsAndCalls = "Songs and calls"
	sectionSongAndCalls  = "Song and calls"
	sectionVocalisation  = "Vocalisation"
	sectionVoice         = "Voice"

	// Multi-language section heading constants shared across locale maps.
	sectionNameVoz      = "Voz"  // Spanish/Portuguese for "Voice"
	sectionNameOpisLang = "Opis" // Polish/Slovak for "Description"

	// User-Agent following Wikimedia policy
	wikiUserAgent = "BirdNETGo/1.0 (https://github.com/tphakala/birdnet-go) Go-HTTP-Client"

	// Circuit breaker durations
	cbRateLimitDuration = 60 * time.Second
	cbBlockedDuration   = 5 * time.Minute
	cbUnavailDuration   = 30 * time.Second
	cbNetworkDuration   = 2 * time.Minute

	// HTTP configuration
	wikiHTTPTimeout     = 30 * time.Second
	wikiIdleConnTimeout = 90 * time.Second

	// Rate limiting
	wikiRateLimitPerSec = 1

	// Response limits
	wikiMaxResponseBody = 1024 * 1024 // 1MB max response body (full extracts are larger)

	// Wikipedia API result status constants
	WikiResultSuccess   = "success"
	WikiResultNotFound  = "not_found"
	WikiResultRateLimit = "rate_limited"
	WikiResultError     = "error"
)

// identificationSections lists English Wikipedia section headings that contain
// bird identification information, in priority order.
var identificationSections = []string{
	sectionDescription,
	sectionSongsAndCalls,
	sectionSongAndCalls,
	sectionVocalisation,
	"Vocalisations",
	"Vocalization",
	"Vocalizations",
	sectionVoice,
	"Similar species",
}

// localeSections holds Wikipedia section headings for a single locale, grouped
// by purpose. Identification is the union used for guide extraction; Description,
// SongCalls, and SimilarSpecies are exposed via the species comparison API.
type localeSections struct {
	Identification []string
	Description    []string
	SongCalls      []string
	SimilarSpecies []string
}

// englishSections is returned for locale "en" or "".
var englishSections = localeSections{
	Identification: identificationSections,
	Description:    []string{sectionDescription},
	SongCalls: []string{
		sectionSongsAndCalls, sectionSongAndCalls, sectionVocalisation,
		"Vocalisations", "Vocalization", "Vocalizations", sectionVoice,
	},
	SimilarSpecies: []string{"Similar species", "Similar Species"},
}

// fallbackSongCalls is returned for unsupported locales — a short English list
// (narrower than englishSections.SongCalls so non-English pages don't match
// English sub-headings spuriously).
var fallbackSongCalls = []string{sectionSongsAndCalls, sectionSongAndCalls, sectionVocalisation, sectionVoice}

// localizedSections maps locale codes to their per-purpose section headings.
// Languages not listed here only get the intro paragraph for identification,
// and English fallbacks for Description/SongCalls.
var localizedSections = map[string]localeSections{
	"de": {
		Identification: []string{"Beschreibung", "Merkmale", "Stimme", "Aussehen", "Verwechslungsmöglichkeiten", "Ähnliche Arten"},
		Description:    []string{"Beschreibung", "Merkmale", "Aussehen"},
		SongCalls:      []string{"Stimme"},
		SimilarSpecies: []string{"Verwechslungsmöglichkeiten", "Ähnliche Arten"},
	},
	"fr": {
		Identification: []string{sectionDescription, "Chant et cris", "Voix", "Plumage", "Espèces similaires"},
		Description:    []string{sectionDescription, "Plumage"},
		SongCalls:      []string{"Chant et cris", "Voix"},
		SimilarSpecies: []string{"Espèces similaires"},
	},
	"es": {
		Identification: []string{"Descripción", sectionNameVoz, sectionNameCanto, "Vocalización", "Especies similares"},
		Description:    []string{"Descripción"},
		SongCalls:      []string{sectionNameVoz, sectionNameCanto, "Vocalización"},
		SimilarSpecies: []string{"Especies similares"},
	},
	"nl": {
		Identification: []string{"Beschrijving", "Geluid", "Stem", "Herkenning"},
		Description:    []string{"Beschrijving", "Herkenning"},
		SongCalls:      []string{"Geluid", "Stem"},
	},
	"pl": {
		Identification: []string{sectionNameOpisLang, "Wygląd", "Głos", "Odgłosy"},
		Description:    []string{sectionNameOpisLang, "Wygląd"},
		SongCalls:      []string{"Głos", "Odgłosy"},
	},
	"pt": {
		Identification: []string{"Descrição", "Vocalização", sectionNameCanto, sectionNameVoz},
		Description:    []string{"Descrição"},
		SongCalls:      []string{"Vocalização", sectionNameCanto, sectionNameVoz},
	},
	"it": {
		Identification: []string{"Descrizione", "Voce", sectionNameCanto, "Piumaggio"},
		Description:    []string{"Descrizione", "Piumaggio"},
		SongCalls:      []string{"Voce", sectionNameCanto},
	},
	"sv": {
		Identification: []string{"Utseende", "Läte", "Kännetecken"},
		Description:    []string{"Utseende", "Kännetecken"},
		SongCalls:      []string{"Läte"},
	},
	"da": {
		Identification: []string{"Udseende", "Stemme", "Kendetegn"},
		Description:    []string{"Udseende", "Kendetegn"},
		SongCalls:      []string{"Stemme"},
	},
	"fi": {
		Identification: []string{"Kuvaus", "Ääntelyt", "Ulkonäkö"},
		Description:    []string{"Kuvaus", "Ulkonäkö"},
		SongCalls:      []string{"Ääntelyt"},
	},
	"hu": {
		Identification: []string{"Leírás", "Megjelenés", "Hang"},
		Description:    []string{"Leírás", "Megjelenés"},
		SongCalls:      []string{"Hang"},
	},
	"sk": {
		Identification: []string{sectionNameOpisLang, "Hlas", "Vzhľad"},
		Description:    []string{sectionNameOpisLang, "Vzhľad"},
		SongCalls:      []string{"Hlas"},
	},
	"lv": {
		Identification: []string{"Apraksts", "Balss", "Izskats"},
		Description:    []string{"Apraksts", "Izskats"},
		SongCalls:      []string{"Balss"},
	},
}

// sectionsFor returns the localeSections for the given locale and whether the
// locale is recognized. The English defaults are returned for "" or "en".
func sectionsFor(locale string) (localeSections, bool) {
	if locale == "" || locale == defaultLocale {
		return englishSections, true
	}
	s, ok := localizedSections[locale]
	return s, ok
}

// DescriptionSectionNames returns the Wikipedia section headings for a species'
// physical appearance for the given locale. Falls back to English for
// unsupported locales.
func DescriptionSectionNames(locale string) []string {
	if s, ok := sectionsFor(locale); ok {
		return s.Description
	}
	return englishSections.Description
}

// SongCallSectionNames returns the Wikipedia section headings for a species'
// songs and calls for the given locale. Falls back to a short English list for
// unsupported locales.
func SongCallSectionNames(locale string) []string {
	if s, ok := sectionsFor(locale); ok {
		return s.SongCalls
	}
	return fallbackSongCalls
}

// SimilarSpeciesSectionNames returns the Wikipedia section headings for a species'
// similar/confusable species for the given locale. Falls back to the English
// headings for unsupported locales (and for locales without a dedicated similar-
// species section, since callers typically also need the English variants).
func SimilarSpeciesSectionNames(locale string) []string {
	if s, ok := sectionsFor(locale); ok && len(s.SimilarSpecies) > 0 {
		return s.SimilarSpecies
	}
	return englishSections.SimilarSpecies
}

// getIdentificationSections returns the section names to look for based on
// locale, or nil for unsupported locales (so only the intro paragraph is used).
func getIdentificationSections(locale string) []string {
	s, ok := sectionsFor(locale)
	if !ok {
		return nil
	}
	return s.Identification
}

// sectionHeaderRe matches Wikipedia section headers like "== Description ==" or "=== Subsection ===".
var sectionHeaderRe = regexp.MustCompile(`(?m)^={2,4}\s*(.+?)\s*={2,4}\s*$`)

// referenceCleanupRe matches inline reference markers like [1], [2], etc.
var referenceCleanupRe = regexp.MustCompile(`\[\d+\]`)

// wikipediaURLs returns the REST summary base URL and action API URL for a locale.
// The locale is validated against the known-safe allowlist to prevent SSRF via subdomain injection.
func wikipediaURLs(locale string) (restBase, actionAPI string) {
	if locale == "" || locale == defaultLocale {
		locale = defaultLocale
	} else if _, ok := localizedSections[locale]; !ok {
		// Unknown locale — fall back to English rather than passing arbitrary input
		// into the URL subdomain (e.g., "evil.com" → https://evil.com.wikipedia.org/…).
		locale = defaultLocale
	}
	return fmt.Sprintf(wikipediaRESTTemplate, locale), fmt.Sprintf(wikipediaActionTemplate, locale)
}

// wikipediaSummaryResponse represents the Wikipedia REST API summary response.
type wikipediaSummaryResponse struct {
	Type        string `json:"type"` // "standard", "disambiguation", "no-extract", etc.
	Title       string `json:"title"`
	Extract     string `json:"extract"` // Plain text summary
	ContentURLs struct {
		Desktop struct {
			Page string `json:"page"`
		} `json:"desktop"`
	} `json:"content_urls"`
}

// wikipediaExtractResponse represents the MediaWiki action API extracts response.
type wikipediaExtractResponse struct {
	Query struct {
		Pages map[string]struct {
			Extract string `json:"extract"`
		} `json:"pages"`
	} `json:"query"`
}

// WikipediaGuideProvider fetches species guide text from the Wikipedia API.
type WikipediaGuideProvider struct {
	httpClient *http.Client
	limiter    *rate.Limiter
	metrics    GuideCacheMetrics

	// Circuit breaker
	circuitMu        sync.RWMutex
	circuitOpenUntil time.Time
	circuitFailures  int    // Number of consecutive failures
	circuitLastError string // Last error message for logging

	// urlsFunc resolves the REST and action API base URLs for a locale.
	// Defaults to wikipediaURLs; tests inject a stub pointing at httptest.Server.
	urlsFunc func(locale string) (restBase, actionAPI string)
}

// NewWikipediaGuideProvider creates a new WikipediaGuideProvider.
// Pass nil for metrics to opt out of metrics recording.
func NewWikipediaGuideProvider(metrics GuideCacheMetrics) *WikipediaGuideProvider {
	transport := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    wikiIdleConnTimeout,
		DisableCompression: false,
	}

	return &WikipediaGuideProvider{
		httpClient: &http.Client{
			Timeout:   wikiHTTPTimeout,
			Transport: transport,
		},
		limiter:  rate.NewLimiter(rate.Limit(wikiRateLimitPerSec), 1),
		metrics:  metrics,
		urlsFunc: wikipediaURLs,
	}
}

// Fetch retrieves species guide information from Wikipedia.
// It first gets the summary (intro paragraph + article URL), then fetches
// the full plain-text extract to pull out identification-relevant sections
// like Description, Songs and calls, and Similar species.
// The locale in opts selects the Wikipedia language edition.
func (p *WikipediaGuideProvider) Fetch(ctx context.Context, scientificName string, opts FetchOptions) (SpeciesGuide, error) {
	log := getLogger()
	locale := opts.Locale
	if locale == "" {
		locale = defaultLocale
	}

	// Check circuit breaker
	if open, reason := p.isCircuitOpen(); open {
		log.Debug("Wikipedia guide circuit breaker open",
			logger.String("reason", reason),
			logger.String("species", scientificName))
		return SpeciesGuide{}, ErrAllProvidersUnavailable
	}

	// Rate limit
	if err := p.limiter.Wait(ctx); err != nil {
		return SpeciesGuide{}, errors.Newf("rate limiter: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	// Step 1: Get the summary (intro + metadata like article URL).
	summary, err := p.fetchSummary(ctx, scientificName, locale)
	if err != nil {
		// Distinguish between definitive not-found and transient/provider errors.
		isNotFound := errors.Is(err, ErrGuideNotFound)

		// If non-English locale failed and it's a definitive not-found, try falling back to English.
		switch {
		case locale != defaultLocale && isNotFound:
			log.Debug("Species not found in localized Wikipedia, trying English",
				logger.String("locale", locale),
				logger.String("species", scientificName))
			summary, err = p.fetchSummary(ctx, scientificName, defaultLocale)
			if err != nil {
				// Check if English fallback also returned not-found or transient error
				if errors.Is(err, ErrGuideNotFound) {
					return SpeciesGuide{}, ErrGuideNotFound
				}
				// Transient error on English fallback - propagate with context
				log.Debug("English Wikipedia lookup failed (transient)",
					logger.String("locale", defaultLocale),
					logger.String("species", scientificName),
					logger.Any("error", err))
				return SpeciesGuide{}, err
			}
			// Reset locale to English since we fell back
			locale = defaultLocale
		case locale != defaultLocale && !isNotFound:
			// Transient error on non-English locale - propagate instead of trying fallback
			log.Debug("Transient error on localized Wikipedia lookup",
				logger.String("locale", locale),
				logger.String("species", scientificName),
				logger.Any("error", err))
			return SpeciesGuide{}, err
		default:
			// English locale (or already tried fallback) with any error
			if !isNotFound {
				log.Debug("Transient error on English Wikipedia lookup",
					logger.String("species", scientificName),
					logger.Any("error", err))
			}
			return SpeciesGuide{}, err
		}
	}

	// Step 2: Fetch the full extract to get identification sections.
	// This is best-effort — if it fails, we still have the summary.
	sectionNames := getIdentificationSections(locale)
	fullDescription := p.buildRichDescription(ctx, summary.Title, summary.Extract, locale, sectionNames)

	guide := SpeciesGuide{
		ScientificName: scientificName,
		CommonName:     summary.Title,
		Description:    fullDescription,
		SourceProvider: WikipediaProviderName,
		SourceURL:      summary.ContentURLs.Desktop.Page,
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		CachedAt:       time.Now(),
		Partial:        false,
	}

	return guide, nil
}

// buildRichDescription fetches the full article extract and combines the intro
// with identification-relevant sections (Description, Songs and calls, etc.).
// Falls back to just the intro summary if the full extract isn't available.
// If sectionNames is nil (unsupported locale), only the intro is returned.
func (p *WikipediaGuideProvider) buildRichDescription(ctx context.Context, title, introText, locale string, sectionNames []string) string {
	log := getLogger()

	// If no section names for this locale, just return the intro.
	if sectionNames == nil {
		return truncate(introText, maxDescriptionLength)
	}

	fullExtract, err := p.fetchFullExtract(ctx, title, locale)
	if err != nil {
		log.Debug("Failed to fetch full Wikipedia extract, using summary only",
			logger.String("title", title),
			logger.String("locale", locale),
			logger.Any("error", err))
		return truncate(introText, maxDescriptionLength)
	}

	// Parse sections from the full extract.
	sections := parseSections(fullExtract)

	// Build a combined description from relevant sections.
	var parts []string
	parts = append(parts, strings.TrimSpace(introText))

	for _, sectionName := range sectionNames {
		if content, ok := sections[strings.ToLower(sectionName)]; ok {
			cleaned := strings.TrimSpace(content)
			if cleaned != "" {
				parts = append(parts, fmt.Sprintf("## %s\n%s", sectionName, cleaned))
			}
		}
	}

	combined := strings.Join(parts, "\n\n")

	// Increase limit for rich descriptions — we have much more useful content now.
	return truncate(combined, maxRichDescriptionLength)
}

// fetchSummary fetches the Wikipedia REST API summary for a given title and locale.
func (p *WikipediaGuideProvider) fetchSummary(ctx context.Context, title, locale string) (*wikipediaSummaryResponse, error) {
	start := time.Now()
	var fetchErr error
	defer func() {
		if p.metrics != nil {
			result := WikiResultSuccess
			if fetchErr != nil {
				if errors.Is(fetchErr, ErrGuideNotFound) {
					result = WikiResultNotFound
				} else {
					result = WikiResultError
				}
			}
			p.metrics.RecordWikipediaAPICall("summary", result, time.Since(start).Seconds())
		}
	}()

	restBase, _ := p.urlsFunc(locale)
	encodedTitle := url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	apiURL := fmt.Sprintf("%s/%s", restBase, encodedTitle)

	body, err := p.doWikiRequest(ctx, apiURL, "summary")
	if err != nil {
		fetchErr = err
		return nil, err
	}

	var summary wikipediaSummaryResponse
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, errors.Newf("parsing summary response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryProcessing).
			Build()
	}

	if summary.Type == "disambiguation" {
		fetchErr = ErrGuideNotFound
		return nil, ErrGuideNotFound
	}
	if summary.Extract == "" {
		fetchErr = ErrGuideNotFound
		return nil, ErrGuideNotFound
	}

	return &summary, nil
}

// fetchFullExtract uses the MediaWiki action API to get the full plain-text
// extract of an article, including all sections.
// It trips the circuit breaker on network and HTTP errors (rate limiting, blocking, etc.)
// and records metrics for every call.
func (p *WikipediaGuideProvider) fetchFullExtract(ctx context.Context, title, locale string) (string, error) {
	start := time.Now()
	var fetchErr error
	defer func() {
		if p.metrics != nil {
			result := WikiResultSuccess
			if fetchErr != nil {
				if errors.Is(fetchErr, ErrGuideNotFound) {
					result = WikiResultNotFound
				} else {
					result = WikiResultError
				}
			}
			p.metrics.RecordWikipediaAPICall("extract", result, time.Since(start).Seconds())
		}
	}()

	_, baseURL := p.urlsFunc(locale)

	params := url.Values{
		"action":          {"query"},
		"titles":          {title},
		"prop":            {"extracts"},
		"explaintext":     {"true"},
		"exsectionformat": {"wiki"},
		"format":          {"json"},
	}
	apiURL := baseURL + "?" + params.Encode()

	// Rate limit the second request too.
	if err := p.limiter.Wait(ctx); err != nil {
		fetchErr = err
		return "", errors.Newf("rate limiter: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	body, err := p.doWikiRequest(ctx, apiURL, "extract")
	if err != nil {
		fetchErr = err
		return "", err
	}

	var extractResp wikipediaExtractResponse
	if err := json.Unmarshal(body, &extractResp); err != nil {
		fetchErr = err
		return "", errors.Newf("parsing extract response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryProcessing).
			Build()
	}

	// The pages map is keyed by page ID (a string number).
	for _, page := range extractResp.Query.Pages {
		if page.Extract != "" {
			return page.Extract, nil
		}
	}

	fetchErr = ErrGuideNotFound
	return "", ErrGuideNotFound
}

// doWikiRequest sends a GET request to apiURL with standard Wikipedia headers,
// reads up to wikiMaxResponseBody bytes, and handles circuit breaker logic on
// network/HTTP failures. label prefixes returned errors and circuit-breaker
// reasons (e.g., "summary" or "extract").
func (p *WikipediaGuideProvider) doWikiRequest(ctx context.Context, apiURL, label string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody)
	if err != nil {
		return nil, errors.Newf("creating %s request: %w", label, err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	req.Header.Set("User-Agent", wikiUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		// Don't trip circuit breaker for context cancellations (caller timeout) — only for actual provider errors.
		// If ctx.Err() == nil and we get DeadlineExceeded, it's the internal client timeout (provider is slow) → trip the breaker.
		if !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			p.tripCircuitBreaker(cbNetworkDuration, label+" network error: "+err.Error())
		}
		return nil, errors.Newf("%s HTTP request failed: %w", label, err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are not actionable after successful read

	if err := p.handleHTTPError(resp); err != nil {
		return nil, err
	}
	p.resetCircuit()

	body, err := io.ReadAll(io.LimitReader(resp.Body, wikiMaxResponseBody))
	if err != nil {
		return nil, errors.Newf("reading %s response: %w", label, err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	return body, nil
}

// parseSections splits a Wikipedia plain-text extract into sections by header.
// Returns a map of lowercase section name → section body text.
func parseSections(extract string) map[string]string {
	sections := make(map[string]string)

	matches := sectionHeaderRe.FindAllStringSubmatchIndex(extract, -1)
	if len(matches) == 0 {
		return sections
	}

	for i, match := range matches {
		// match[2]:match[3] is the capture group (section name)
		name := strings.ToLower(strings.TrimSpace(extract[match[2]:match[3]]))

		// Section body runs from end of this header to start of next header (or end of text).
		bodyStart := match[1] // End of the full match
		var bodyEnd int
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0] // Start of the next header
		} else {
			bodyEnd = len(extract)
		}

		body := strings.TrimSpace(extract[bodyStart:bodyEnd])
		// Clean up reference markers like [1], [2].
		body = referenceCleanupRe.ReplaceAllString(body, "")
		sections[name] = body
	}

	return sections
}

// handleHTTPError checks the HTTP response status and trips the circuit breaker as needed.
func (p *WikipediaGuideProvider) handleHTTPError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrGuideNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		p.tripCircuitBreaker(cbRateLimitDuration, "rate limited")
		return errors.Newf("Wikipedia rate limited").
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode == http.StatusForbidden:
		p.tripCircuitBreaker(cbBlockedDuration, "access blocked")
		return errors.Newf("Wikipedia access blocked").
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode == http.StatusServiceUnavailable:
		p.tripCircuitBreaker(cbUnavailDuration, "service unavailable")
		return errors.Newf("Wikipedia service unavailable").
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode >= 500 && resp.StatusCode < 600:
		// Any 5xx status is a transient server error that should trip the breaker
		p.tripCircuitBreaker(cbUnavailDuration, fmt.Sprintf("server error %d", resp.StatusCode))
		return errors.Newf("Wikipedia server error: status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode != http.StatusOK:
		return errors.Newf("Wikipedia returned status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	return nil
}

// isCircuitOpen checks if the circuit breaker is blocking requests.
func (p *WikipediaGuideProvider) isCircuitOpen() (open bool, reason string) {
	p.circuitMu.RLock()
	defer p.circuitMu.RUnlock()

	if time.Now().Before(p.circuitOpenUntil) {
		return true, p.circuitLastError
	}
	return false, ""
}

// tripCircuitBreaker opens the circuit breaker for the specified duration.
func (p *WikipediaGuideProvider) tripCircuitBreaker(duration time.Duration, reason string) {
	p.circuitMu.Lock()
	defer p.circuitMu.Unlock()

	p.circuitOpenUntil = time.Now().Add(duration)
	p.circuitFailures++
	p.circuitLastError = reason

	getLogger().Error("Opening Wikipedia guide circuit breaker",
		logger.String("reason", reason),
		logger.Duration("duration", duration),
		logger.Int("consecutive_failures", p.circuitFailures))
}

// resetCircuit resets the circuit breaker on successful request.
func (p *WikipediaGuideProvider) resetCircuit() {
	p.circuitMu.Lock()
	defer p.circuitMu.Unlock()

	if p.circuitFailures > 0 {
		getLogger().Info("Resetting Wikipedia guide circuit breaker after successful request",
			logger.Int("previous_failures", p.circuitFailures))
	}

	p.circuitOpenUntil = time.Time{}
	p.circuitFailures = 0
	p.circuitLastError = ""
}

// truncate shortens text to maxLen, breaking at a word boundary.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last space before maxLen to avoid cutting mid-word.
	idx := strings.LastIndex(s[:maxLen], " ")
	if idx < maxLen/2 {
		idx = maxLen
	}
	return TruncateUTF8(s, idx, "...")
}
