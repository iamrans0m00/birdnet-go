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
)

// identificationSections lists English Wikipedia section headings that contain
// bird identification information, in priority order.
var identificationSections = []string{
	"Description",
	"Songs and calls",
	"Song and calls",
	"Vocalisation",
	"Vocalisations",
	"Vocalization",
	"Vocalizations",
	"Voice",
	"Similar species",
}

// localizedSectionNames maps locale codes to their identification section headings.
// Languages not listed here will only get the intro paragraph (no section extraction).
var localizedSectionNames = map[string][]string{
	"de": {"Beschreibung", "Merkmale", "Stimme", "Aussehen", "Verwechslungsmöglichkeiten", "Ähnliche Arten"},
	"fr": {"Description", "Chant et cris", "Voix", "Plumage", "Espèces similaires"},
	"es": {"Descripción", "Voz", "Canto", "Vocalización", "Especies similares"},
	"nl": {"Beschrijving", "Geluid", "Stem", "Herkenning"},
	"pl": {"Opis", "Wygląd", "Głos", "Odgłosy"},
	"pt": {"Descrição", "Vocalização", "Canto", "Voz"},
	"it": {"Descrizione", "Voce", "Canto", "Piumaggio"},
	"sv": {"Utseende", "Läte", "Kännetecken"},
	"da": {"Udseende", "Stemme", "Kendetegn"},
	"fi": {"Kuvaus", "Ääntelyt", "Ulkonäkö"},
	"hu": {"Leírás", "Megjelenés", "Hang"},
	"sk": {"Opis", "Hlas", "Vzhľad"},
	"lv": {"Apraksts", "Balss", "Izskats"},
}

// getIdentificationSections returns the section names to look for based on locale.
func getIdentificationSections(locale string) []string {
	if locale == "" || locale == defaultLocale {
		return identificationSections
	}
	if sections, ok := localizedSectionNames[locale]; ok {
		return sections
	}
	// Unsupported locale — return nil so we only use the intro paragraph.
	return nil
}

// sectionHeaderRe matches Wikipedia section headers like "== Description ==" or "=== Subsection ===".
var sectionHeaderRe = regexp.MustCompile(`(?m)^={2,4}\s*(.+?)\s*={2,4}\s*$`)

// referenceCleanupRe matches inline reference markers like [1], [2], etc.
var referenceCleanupRe = regexp.MustCompile(`\[\d+\]`)

// wikipediaURLs returns the REST summary base URL and action API URL for a locale.
func wikipediaURLs(locale string) (restBase, actionAPI string) {
	if locale == "" {
		locale = defaultLocale
	}
	return fmt.Sprintf(wikipediaRESTTemplate, locale), fmt.Sprintf(wikipediaActionTemplate, locale)
}

// wikipediaSummaryResponse represents the Wikipedia REST API summary response.
type wikipediaSummaryResponse struct {
	Type        string `json:"type"`    // "standard", "disambiguation", "no-extract", etc.
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

	// Circuit breaker
	circuitMu        sync.RWMutex
	circuitOpenUntil time.Time
	circuitFailures  int    // Number of consecutive failures
	circuitLastError string // Last error message for logging

	// testBaseURL overrides the Wikipedia API base URL for testing.
	testBaseURL string
}

// NewWikipediaGuideProvider creates a new WikipediaGuideProvider.
func NewWikipediaGuideProvider() *WikipediaGuideProvider {
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
		limiter: rate.NewLimiter(rate.Limit(wikiRateLimitPerSec), 1),
	}
}

// Fetch retrieves species guide information from Wikipedia.
// It first gets the summary (intro paragraph + article URL), then fetches
// the full plain-text extract to pull out identification-relevant sections
// like Description, Songs and calls, and Similar species.
// The locale in opts selects the Wikipedia language edition.
func (p *WikipediaGuideProvider) Fetch(ctx context.Context, scientificName string, opts FetchOptions) (SpeciesGuide, error) {
	log := GetLogger()
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
		// If non-English locale failed, try falling back to English.
		if locale != defaultLocale {
			log.Debug("Localized Wikipedia lookup failed, falling back to English",
				logger.String("locale", locale),
				logger.String("species", scientificName))
			summary, err = p.fetchSummary(ctx, scientificName, defaultLocale)
			if err != nil {
				return SpeciesGuide{}, ErrGuideNotFound
			}
			// Reset locale to English since we fell back
			locale = defaultLocale
		} else {
			log.Debug("Wikipedia scientific name lookup failed",
				logger.String("species", scientificName),
				logger.Any("error", err))
			return SpeciesGuide{}, ErrGuideNotFound
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
	log := GetLogger()

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
	restBase, _ := wikipediaURLs(locale)
	baseURL := restBase
	if p.testBaseURL != "" {
		baseURL = p.testBaseURL
	}
	encodedTitle := url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	apiURL := fmt.Sprintf("%s/%s", baseURL, encodedTitle)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, errors.Newf("creating request: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	req.Header.Set("User-Agent", wikiUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.tripCircuitBreaker(cbNetworkDuration, "network error: "+err.Error())
		return nil, errors.Newf("HTTP request failed: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	defer resp.Body.Close()

	if err := p.handleHTTPError(resp); err != nil {
		return nil, err
	}

	// Reset circuit breaker on successful response
	p.resetCircuit()

	body, err := io.ReadAll(io.LimitReader(resp.Body, wikiMaxResponseBody))
	if err != nil {
		return nil, errors.Newf("reading response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	var summary wikipediaSummaryResponse
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, errors.Newf("parsing response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryProcessing).
			Build()
	}

	if summary.Type == "disambiguation" {
		return nil, ErrGuideNotFound
	}
	if summary.Extract == "" {
		return nil, ErrGuideNotFound
	}

	return &summary, nil
}

// fetchFullExtract uses the MediaWiki action API to get the full plain-text
// extract of an article, including all sections.
func (p *WikipediaGuideProvider) fetchFullExtract(ctx context.Context, title, locale string) (string, error) {
	_, actionBase := wikipediaURLs(locale)
	baseURL := actionBase
	if p.testBaseURL != "" {
		// In tests, the action API is at testBaseURL + "/w/api.php"
		// but for simplicity, tests can override this separately.
		baseURL = p.testBaseURL + "/w/api.php"
	}

	params := url.Values{
		"action":          {"query"},
		"titles":          {title},
		"prop":            {"extracts"},
		"explaintext":     {"true"},
		"exsectionformat": {"wiki"},
		"format":          {"json"},
	}
	apiURL := baseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", errors.Newf("creating extract request: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	req.Header.Set("User-Agent", wikiUserAgent)
	req.Header.Set("Accept", "application/json")

	// Rate limit the second request too.
	if err := p.limiter.Wait(ctx); err != nil {
		return "", errors.Newf("rate limiter: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", errors.Newf("extract HTTP request failed: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.Newf("extract API returned status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, wikiMaxResponseBody))
	if err != nil {
		return "", errors.Newf("reading extract response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	var extractResp wikipediaExtractResponse
	if err := json.Unmarshal(body, &extractResp); err != nil {
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

	return "", ErrGuideNotFound
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
	case resp.StatusCode != http.StatusOK:
		return errors.Newf("Wikipedia returned status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	return nil
}

// isCircuitOpen checks if the circuit breaker is blocking requests.
func (p *WikipediaGuideProvider) isCircuitOpen() (bool, string) {
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

	GetLogger().Error("Opening Wikipedia guide circuit breaker",
		logger.String("reason", reason),
		logger.Duration("duration", duration),
		logger.Int("consecutive_failures", p.circuitFailures))
}

// resetCircuit resets the circuit breaker on successful request.
func (p *WikipediaGuideProvider) resetCircuit() {
	p.circuitMu.Lock()
	defer p.circuitMu.Unlock()

	if p.circuitFailures > 0 {
		GetLogger().Info("Resetting Wikipedia guide circuit breaker after successful request",
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
		idx = maxLen // No good break point, just cut.
	}
	return s[:idx] + "..."
}
