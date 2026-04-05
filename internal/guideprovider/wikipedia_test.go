package guideprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wikiTypeStandard = "standard"

func TestWikipediaGuideProvider_Fetch_Success(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()

	// REST summary endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:    wikiTypeStandard,
			Title:   "Common blackbird",
			Extract: "The common blackbird is a species of true thrush.",
		}
		response.ContentURLs.Desktop.Page = "https://en.wikipedia.org/wiki/Common_blackbird"

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// Action API extract endpoint
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, _ *http.Request) {
		resp := wikipediaExtractResponse{}
		resp.Query.Pages = map[string]struct {
			Extract string `json:"extract"`
		}{
			"12345": {
				Extract: "The common blackbird is a species of true thrush.\n\n" +
					"== Description ==\n" +
					"The adult male is 24-27 cm long with a glossy black plumage and orange-yellow bill.\n\n" +
					"== Songs and calls ==\n" +
					"The male sings a rich melodious fluting song from treetops.\n\n" +
					"== Distribution ==\nWidely distributed across Europe.",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	guide, err := provider.Fetch(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)

	assert.Equal(t, "Common blackbird", guide.CommonName)
	assert.Equal(t, "Turdus merula", guide.ScientificName)
	assert.Equal(t, WikipediaProviderName, guide.SourceProvider)
	assert.Equal(t, "CC BY-SA 4.0", guide.LicenseName)

	// Should contain the intro, description, and songs sections.
	assert.Contains(t, guide.Description, "species of true thrush")
	assert.Contains(t, guide.Description, "## Description")
	assert.Contains(t, guide.Description, "glossy black plumage")
	assert.Contains(t, guide.Description, "## Songs and calls")
	assert.Contains(t, guide.Description, "rich melodious fluting")

	// Should NOT contain non-identification sections.
	assert.NotContains(t, guide.Description, "## Distribution")
}

func TestWikipediaGuideProvider_Fetch_FallbackToSummary(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()

	// REST summary endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:    wikiTypeStandard,
			Title:   "Test bird",
			Extract: "A short summary about a bird.",
		}
		response.ContentURLs.Desktop.Page = "https://en.wikipedia.org/wiki/Test_bird"
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// Action API returns error
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	guide, err := provider.Fetch(t.Context(), "Test species", FetchOptions{})
	require.NoError(t, err)

	// Should fall back to the summary extract.
	assert.Equal(t, "A short summary about a bird.", guide.Description)
}

func TestWikipediaGuideProvider_Fetch_NotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	_, err := provider.Fetch(t.Context(), "Nonexistent species", FetchOptions{})
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestWikipediaGuideProvider_Fetch_Disambiguation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:  "disambiguation",
			Title: "Blackbird",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	_, err := provider.Fetch(t.Context(), "Blackbird", FetchOptions{})
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestWikipediaGuideProvider_Fetch_RateLimited(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	_, err := provider.fetchSummary(t.Context(), "Turdus merula", "en")
	require.Error(t, err)

	// Circuit breaker should be tripped
	open, reason := provider.isCircuitOpen()
	assert.True(t, open)
	assert.Equal(t, "rate limited", reason)
}

func TestWikipediaGuideProvider_Fetch_EmptyExtract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:    wikiTypeStandard,
			Title:   "Some page",
			Extract: "",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	_, err := provider.Fetch(t.Context(), "Empty page", FetchOptions{})
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestWikipediaGuideProvider_CircuitBreaker(t *testing.T) {
	t.Parallel()

	provider := NewWikipediaGuideProvider()

	// Initially closed
	open, _ := provider.isCircuitOpen()
	assert.False(t, open)

	// Trip it
	provider.tripCircuitBreaker(1*time.Minute, "test reason")
	open, reason := provider.isCircuitOpen()
	assert.True(t, open)
	assert.Equal(t, "test reason", reason)
	assert.Equal(t, 1, provider.circuitFailures)

	// Trip again to verify consecutive failure tracking
	provider.tripCircuitBreaker(1*time.Minute, "second failure")
	assert.Equal(t, 2, provider.circuitFailures)

	// Reset on success
	provider.resetCircuit()
	open, _ = provider.isCircuitOpen()
	assert.False(t, open)
	assert.Equal(t, 0, provider.circuitFailures)
	assert.Empty(t, provider.circuitLastError)
}

func TestParseSections(t *testing.T) {
	t.Parallel()

	extract := `The intro paragraph.

== Description ==
The bird is 24-27 cm long with black plumage.

== Songs and calls ==
It sings a rich melodious song.

== Distribution and habitat ==
Found across Europe.

== Similar species ==
Closely resembles the ring ouzel.`

	sections := parseSections(extract)

	assert.Contains(t, sections, "description")
	assert.Contains(t, sections["description"], "24-27 cm")

	assert.Contains(t, sections, "songs and calls")
	assert.Contains(t, sections["songs and calls"], "melodious song")

	assert.Contains(t, sections, "similar species")
	assert.Contains(t, sections["similar species"], "ring ouzel")

	assert.Contains(t, sections, "distribution and habitat")
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	// Short text — no truncation.
	assert.Equal(t, "hello", truncate("hello", 100))

	// Long text — truncated at word boundary.
	long := "word1 word2 word3 word4 word5"
	result := truncate(long, 15)
	assert.True(t, strings.HasSuffix(result, "..."))
	assert.LessOrEqual(t, len(result), 18) // 15 + "..."
}

// newTestWikipediaProvider creates a WikipediaGuideProvider pointing at a test server.
func newTestWikipediaProvider(baseURL string) *WikipediaGuideProvider {
	provider := NewWikipediaGuideProvider()
	provider.testBaseURL = baseURL
	return provider
}
