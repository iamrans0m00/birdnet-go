package guideprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// newEBirdTestClient creates an ebird.Client pointed at a test server.
func newEBirdTestClient(tb testing.TB, server *httptest.Server) *ebird.Client {
	tb.Helper()
	cfg := ebird.Config{
		APIKey:      "test-token",
		BaseURL:     server.URL,
		Timeout:     5 * time.Second,
		CacheTTL:    1 * time.Hour,
		RateLimitMS: 0,
	}
	client, err := ebird.NewClient(cfg)
	require.NoError(tb, err)
	tb.Cleanup(client.Close)
	return client
}

// ebirdTaxonomyServer creates a test HTTP server that returns the given taxonomy entries.
func ebirdTaxonomyServer(tb testing.TB, entries []ebird.TaxonomyEntry) *httptest.Server {
	tb.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(entries)
	}))
	tb.Cleanup(server.Close)
	return server
}

func TestNewEBirdGuideProvider_NilClient(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	p, err := NewEBirdGuideProvider(nil, nil)
	assert.Nil(t, p)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProviderNotConfigured))
}

func TestNewEBirdGuideProvider_ValidClient(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	server := ebirdTaxonomyServer(t, nil)
	client := newEBirdTestClient(t, server)
	p, err := NewEBirdGuideProvider(client, nil)
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestEBirdGuideProvider_Fetch_SpeciesFound(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	entries := []ebird.TaxonomyEntry{
		{
			ScientificName: testSpeciesMerula,
			CommonName:     testCommonBlackbird,
			SpeciesCode:    "combla",
			Category:       "species",
		},
	}

	server := ebirdTaxonomyServer(t, entries)
	client := newEBirdTestClient(t, server)
	p, err := NewEBirdGuideProvider(client, nil)
	require.NoError(t, err)

	guide, err := p.Fetch(t.Context(), testSpeciesMerula, FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, testSpeciesMerula, guide.ScientificName)
	assert.Equal(t, testCommonBlackbird, guide.CommonName)
	assert.Equal(t, EBirdProviderName, guide.SourceProvider)
	assert.True(t, guide.Partial, "eBird guides are always partial (no description)")
	assert.Empty(t, guide.ConservationStatus)
}

func TestEBirdGuideProvider_Fetch_ExtinctSpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	const extinctSpecies = "Raphus cucullatus"
	entries := []ebird.TaxonomyEntry{
		{
			ScientificName: extinctSpecies,
			CommonName:     "Dodo",
			SpeciesCode:    "dodo1",
			Category:       "species",
			Extinct:        true,
			ExtinctYear:    1681,
		},
	}

	server := ebirdTaxonomyServer(t, entries)
	client := newEBirdTestClient(t, server)
	p, err := NewEBirdGuideProvider(client, nil)
	require.NoError(t, err)

	guide, err := p.Fetch(t.Context(), extinctSpecies, FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, extinctSpecies, guide.ScientificName)
	assert.Contains(t, guide.ConservationStatus, "Extinct")
	assert.Contains(t, guide.ConservationStatus, "1681")
}

func TestEBirdGuideProvider_Fetch_NotFound(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	// Taxonomy contains a different species — the queried one is absent.
	entries := []ebird.TaxonomyEntry{
		{ScientificName: testSpeciesParus, CommonName: "Great Tit"},
	}

	server := ebirdTaxonomyServer(t, entries)
	client := newEBirdTestClient(t, server)
	p, err := NewEBirdGuideProvider(client, nil)
	require.NoError(t, err)

	_, err = p.Fetch(t.Context(), testSpeciesMerula, FetchOptions{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrGuideNotFound))
}

func TestEBirdGuideProvider_Fetch_APIError(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"title":"Internal Server Error","status":500}`)
	}))
	t.Cleanup(server.Close)

	client := newEBirdTestClient(t, server)
	p, err := NewEBirdGuideProvider(client, nil)
	require.NoError(t, err)

	_, fetchErr := p.Fetch(t.Context(), testSpeciesMerula, FetchOptions{})
	require.Error(t, fetchErr)
	// Server errors must not be silently converted to ErrGuideNotFound.
	assert.False(t, errors.Is(fetchErr, ErrGuideNotFound))
}

func TestEBirdGuideProvider_Fetch_IgnoresLocale(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "ebird-provider")

	entries := []ebird.TaxonomyEntry{
		{ScientificName: testSpeciesMerula, CommonName: testCommonBlackbird},
	}

	server := ebirdTaxonomyServer(t, entries)
	client := newEBirdTestClient(t, server)
	p, err := NewEBirdGuideProvider(client, nil)
	require.NoError(t, err)

	// eBird always returns English common names regardless of requested locale.
	guide, err := p.Fetch(t.Context(), testSpeciesMerula, FetchOptions{Locale: "de"})
	require.NoError(t, err)
	assert.Equal(t, testCommonBlackbird, guide.CommonName)
}
