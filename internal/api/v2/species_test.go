// species_test.go: Package api provides tests for species-related functions and endpoints.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
)

// TestCalculateRarityStatus tests the calculateRarityStatus helper function.
func TestCalculateRarityStatus(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "rarity-calculation")

	tests := []struct {
		name     string
		score    float64
		expected RarityStatus
	}{
		// Very common (score > 0.8)
		{
			name:     "Very common - score 0.95",
			score:    0.95,
			expected: RarityVeryCommon,
		},
		{
			name:     "Very common - score 0.81",
			score:    0.81,
			expected: RarityVeryCommon,
		},
		{
			name:     "Very common - boundary exactly 0.80001",
			score:    0.80001,
			expected: RarityVeryCommon,
		},

		// Common (0.5 < score <= 0.8)
		{
			name:     "Common - score 0.8 (boundary)",
			score:    0.8, // Exactly at threshold
			expected: RarityCommon,
		},
		{
			name:     "Common - score 0.65",
			score:    0.65,
			expected: RarityCommon,
		},
		{
			name:     "Common - score 0.51",
			score:    0.51,
			expected: RarityCommon,
		},

		// Uncommon (0.2 < score <= 0.5)
		{
			name:     "Uncommon - score 0.5 (boundary)",
			score:    0.5,
			expected: RarityUncommon,
		},
		{
			name:     "Uncommon - score 0.35",
			score:    0.35,
			expected: RarityUncommon,
		},
		{
			name:     "Uncommon - score 0.21",
			score:    0.21,
			expected: RarityUncommon,
		},

		// Rare (0.05 < score <= 0.2)
		{
			name:     "Rare - score 0.2 (boundary)",
			score:    0.2,
			expected: RarityRare,
		},
		{
			name:     "Rare - score 0.1",
			score:    0.1,
			expected: RarityRare,
		},
		{
			name:     "Rare - score 0.051",
			score:    0.051,
			expected: RarityRare,
		},

		// Very rare (score <= 0.05)
		{
			name:     "Very rare - score 0.05 (boundary)",
			score:    0.05,
			expected: RarityVeryRare,
		},
		{
			name:     "Very rare - score 0.01",
			score:    0.01,
			expected: RarityVeryRare,
		},
		{
			name:     "Very rare - score 0",
			score:    0.0,
			expected: RarityVeryRare,
		},

		// Edge cases
		{
			name:     "Score exactly 1.0",
			score:    1.0,
			expected: RarityVeryCommon,
		},
		{
			name:     "Negative score",
			score:    -0.1,
			expected: RarityVeryRare,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := calculateRarityStatus(tt.score)
			assert.Equal(t, tt.expected, result, "Score %.4f should map to %s", tt.score, tt.expected)
		})
	}
}

// TestRarityStatusConstants tests that rarity status constants are correctly defined.
func TestRarityStatusConstants(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "rarity-constants")

	// Test status values
	assert.Equal(t, RarityVeryCommon, RarityStatus("very_common"))
	assert.Equal(t, RarityCommon, RarityStatus("common"))
	assert.Equal(t, RarityUncommon, RarityStatus("uncommon"))
	assert.Equal(t, RarityRare, RarityStatus("rare"))
	assert.Equal(t, RarityVeryRare, RarityStatus("very_rare"))
	assert.Equal(t, RarityUnknown, RarityStatus("unknown"))

	// Test threshold values
	assert.InDelta(t, 0.8, RarityThresholdVeryCommon, 0.001)
	assert.InDelta(t, 0.5, RarityThresholdCommon, 0.001)
	assert.InDelta(t, 0.2, RarityThresholdUncommon, 0.001)
	assert.InDelta(t, 0.05, RarityThresholdRare, 0.001)
}

// TestSpeciesAPIValidation tests validation for all species endpoints in a single table-driven test.
func TestSpeciesAPIValidation(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "api-validation")

	tests := []struct {
		name           string
		url            string
		paramNames     []string
		paramValues    []string
		handler        func(*Controller) func(echo.Context) error
		expectedStatus int
		expectedBody   string
	}{
		// Missing parameter tests
		{"GetSpeciesInfo missing param", "/api/v2/species", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Missing required parameter"},
		{"GetSpeciesTaxonomy missing param", "/api/v2/species/taxonomy", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Missing required parameter"},
		{"GetSpeciesThumbnail missing code", "/api/v2/species//thumbnail", []string{"code"}, []string{""},
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesThumbnail },
			http.StatusBadRequest, "Missing species code"},

		// Invalid format tests - GetSpeciesInfo
		{"GetSpeciesInfo too short", "/api/v2/species?scientific_name=Ab", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesInfo no space", "/api/v2/species?scientific_name=Turdusmigratorius", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesInfo single word", "/api/v2/species?scientific_name=Turdus", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},

		// Invalid format tests - GetSpeciesTaxonomy
		{"GetSpeciesTaxonomy too short", "/api/v2/species/taxonomy?scientific_name=Ab", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesTaxonomy no space", "/api/v2/species/taxonomy?scientific_name=Turdusmigratorius", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesTaxonomy single word", "/api/v2/species/taxonomy?scientific_name=Turdus", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},

		// Error handling - nil processor
		{"GetSpeciesThumbnail nil processor", "/api/v2/species/amro/thumbnail", []string{"code"}, []string{"amro"},
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesThumbnail },
			http.StatusServiceUnavailable, "BirdNET service unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, tt.url, http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			if tt.paramNames != nil {
				ctx.SetParamNames(tt.paramNames...)
				ctx.SetParamValues(tt.paramValues...)
			}

			c := newMinimalController()
			err := tt.handler(c)(ctx)

			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.expectedStatus, rec.Code, tt.name)
			assert.Contains(t, rec.Body.String(), tt.expectedBody, tt.name)
		})
	}
}

// TestSpeciesInfoJSONSerialization tests that SpeciesInfo serializes correctly to JSON.
func TestSpeciesInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	info := SpeciesInfo{
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Rarity: &SpeciesRarityInfo{
			Status:           RarityCommon,
			Score:            0.65,
			LocationBased:    true,
			Latitude:         40.7128,
			Longitude:        -74.006,
			Date:             "2024-01-15",
			ThresholdApplied: 0.03,
		},
		Metadata: map[string]any{
			"source": "local",
		},
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	// Verify JSON structure
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Turdus migratorius", parsed["scientific_name"])
	assert.Equal(t, "American Robin", parsed["common_name"])
	assert.NotNil(t, parsed["rarity"])
	assert.NotNil(t, parsed["metadata"])

	// Verify rarity structure
	rarity, ok := parsed["rarity"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "common", rarity["status"])
	assert.InDelta(t, 0.65, rarity["score"].(float64), 0.001)
}

// TestSpeciesRarityInfoJSONSerialization tests that SpeciesRarityInfo serializes correctly.
func TestSpeciesRarityInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	info := SpeciesRarityInfo{
		Status:           RarityRare,
		Score:            0.08,
		LocationBased:    true,
		Latitude:         60.1699,
		Longitude:        24.9384,
		Date:             "2024-06-15",
		ThresholdApplied: 0.05,
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "rare", parsed["status"])
	assert.InDelta(t, 0.08, parsed["score"].(float64), 0.001)
	assert.Equal(t, true, parsed["location_based"])
	assert.InDelta(t, 60.1699, parsed["latitude"].(float64), 0.001)
	assert.InDelta(t, 24.9384, parsed["longitude"].(float64), 0.001)
	assert.Equal(t, "2024-06-15", parsed["date"])
	assert.InDelta(t, 0.05, parsed["threshold_applied"].(float64), 0.001)
}

// TestTaxonomyHierarchyJSONSerialization tests that TaxonomyHierarchy serializes correctly.
func TestTaxonomyHierarchyJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	hierarchy := TaxonomyHierarchy{
		Kingdom:       "Animalia",
		Phylum:        "Chordata",
		Class:         "Aves",
		Order:         "Passeriformes",
		Family:        "Turdidae",
		FamilyCommon:  "Thrushes and Allies",
		Genus:         "Turdus",
		Species:       "Turdus migratorius",
		SpeciesCommon: "American Robin",
	}

	data, err := json.Marshal(hierarchy)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Animalia", parsed["kingdom"])
	assert.Equal(t, "Chordata", parsed["phylum"])
	assert.Equal(t, "Aves", parsed["class"])
	assert.Equal(t, "Passeriformes", parsed["order"])
	assert.Equal(t, "Turdidae", parsed["family"])
	assert.Equal(t, "Thrushes and Allies", parsed["family_common"])
	assert.Equal(t, "Turdus", parsed["genus"])
	assert.Equal(t, "Turdus migratorius", parsed["species"])
	assert.Equal(t, "American Robin", parsed["species_common"])
}

// TestSubspeciesInfoJSONSerialization tests that SubspeciesInfo serializes correctly.
func TestSubspeciesInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	subspecies := SubspeciesInfo{
		ScientificName: "Turdus migratorius migratorius",
		CommonName:     "Eastern American Robin",
		Region:         "Eastern North America",
	}

	data, err := json.Marshal(subspecies)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Turdus migratorius migratorius", parsed["scientific_name"])
	assert.Equal(t, "Eastern American Robin", parsed["common_name"])
	assert.Equal(t, "Eastern North America", parsed["region"])
}

// TestTaxonomyInfoJSONSerialization tests that TaxonomyInfo serializes correctly.
func TestTaxonomyInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	info := TaxonomyInfo{
		ScientificName: "Turdus migratorius",
		SpeciesCode:    "amero",
		Taxonomy: &TaxonomyHierarchy{
			Kingdom: "Animalia",
			Phylum:  "Chordata",
			Class:   "Aves",
			Order:   "Passeriformes",
			Family:  "Turdidae",
			Genus:   "Turdus",
			Species: "Turdus migratorius",
		},
		Subspecies: []SubspeciesInfo{
			{ScientificName: "Turdus migratorius migratorius", CommonName: "Eastern Robin"},
		},
		Metadata: map[string]any{
			"source": "local",
		},
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Turdus migratorius", parsed["scientific_name"])
	assert.Equal(t, "amero", parsed["species_code"])
	assert.NotNil(t, parsed["taxonomy"])
	assert.NotNil(t, parsed["subspecies"])
	assert.NotNil(t, parsed["metadata"])

	// Verify subspecies array
	subspecies, ok := parsed["subspecies"].([]any)
	require.True(t, ok)
	assert.Len(t, subspecies, 1)
}

// TestGetSpeciesGuide tests the GetSpeciesGuide endpoint.
func TestGetSpeciesGuide(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "species-guide")

	// Save original settings and restore after test
	origSettings := conf.GetSettings()
	t.Cleanup(func() {
		conf.SetTestSettings(origSettings)
	})

	tests := []struct {
		name           string
		scientificName string
		setupCtrl      func(*Controller)
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "feature disabled",
			scientificName: "Turdus merula",
			setupCtrl: func(c *Controller) {
				settings := conf.GetTestSettings()
				settings.Realtime.Dashboard.SpeciesGuide.Enabled = false
				conf.SetTestSettings(settings)
				c.Settings = settings
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody:   "Species guide feature is disabled",
		},
		{
			name:           "nil guide cache",
			scientificName: "Turdus merula",
			setupCtrl: func(c *Controller) {
				settings := conf.GetTestSettings()
				settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
				conf.SetTestSettings(settings)
				c.Settings = settings
				c.GuideCache = nil
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody:   "Species guide service not available",
		},
		{
			name:           "empty scientific name",
			scientificName: "",
			setupCtrl: func(c *Controller) {
				settings := conf.GetTestSettings()
				settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
				conf.SetTestSettings(settings)
				c.Settings = settings
				c.GuideCache = guideprovider.NewGuideCache(nil, nil)
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing required parameter",
		},
		{
			name:           "species not found returns 404",
			scientificName: "Nonexistent species",
			setupCtrl: func(c *Controller) {
				settings := conf.GetTestSettings()
				settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
				settings.Realtime.Dashboard.SpeciesGuide.Provider = guideprovider.WikipediaProviderName
				settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy = guideprovider.FallbackPolicyNone
				conf.SetTestSettings(settings)
				c.Settings = settings
				cache := guideprovider.NewGuideCache(nil, nil)
				cache.RegisterProvider(guideprovider.WikipediaProviderName, &stubGuideProvider{
					err: guideprovider.ErrGuideNotFound,
				})
				c.GuideCache = cache
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "Species guide not found",
		},
		{
			name:           "success returns guide data with quality stub",
			scientificName: "Turdus merula",
			setupCtrl: func(c *Controller) {
				settings := conf.GetTestSettings()
				settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
				settings.Realtime.Dashboard.SpeciesGuide.Provider = guideprovider.WikipediaProviderName
				settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy = guideprovider.FallbackPolicyNone
				conf.SetTestSettings(settings)
				c.Settings = settings
				cache := guideprovider.NewGuideCache(nil, nil)
				cache.RegisterProvider(guideprovider.WikipediaProviderName, &stubGuideProvider{
					guide: guideprovider.SpeciesGuide{
						ScientificName: "Turdus merula",
						CommonName:     "Common Blackbird",
						Description:    "A species of true thrush.",
						SourceProvider: guideprovider.WikipediaProviderName,
						SourceURL:      "https://en.wikipedia.org/wiki/Common_blackbird",
						LicenseName:    "CC BY-SA 4.0",
						LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
						Partial:        true,
					},
				})
				c.GuideCache = cache
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"quality":"stub"`,
		},
		{
			name:           "success returns full quality guide",
			scientificName: "Turdus merula",
			setupCtrl: func(c *Controller) {
				settings := conf.GetTestSettings()
				settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
				settings.Realtime.Dashboard.SpeciesGuide.Provider = guideprovider.WikipediaProviderName
				settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy = guideprovider.FallbackPolicyNone
				conf.SetTestSettings(settings)
				c.Settings = settings
				cache := guideprovider.NewGuideCache(nil, nil)
				cache.RegisterProvider(guideprovider.WikipediaProviderName, &stubGuideProvider{
					guide: guideprovider.SpeciesGuide{
						ScientificName: "Turdus merula",
						CommonName:     "Common Blackbird",
						Description:    "The common blackbird.\n\n## Description\nBlack plumage.\n\n## Songs and calls\nFlute-like song.",
						SourceProvider: guideprovider.WikipediaProviderName,
					},
				})
				c.GuideCache = cache
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"quality":"full"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/species/"+url.PathEscape(tt.scientificName)+"/guide", http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("scientific_name")
			ctx.SetParamValues(tt.scientificName)

			c := &Controller{Echo: e, Group: e.Group("/api/v2")}
			tt.setupCtrl(c)

			err := c.GetSpeciesGuide(ctx)
			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.expectedStatus, rec.Code, tt.name)
			assert.Contains(t, rec.Body.String(), tt.expectedBody, tt.name)
		})
	}
}

// stubGuideProvider is a test double for guideprovider.GuideProvider.
type stubGuideProvider struct {
	guide guideprovider.SpeciesGuide
	err   error
}

func (s *stubGuideProvider) Fetch(_ context.Context, _ string, _ guideprovider.FetchOptions) (guideprovider.SpeciesGuide, error) {
	if s.err != nil {
		return guideprovider.SpeciesGuide{}, s.err
	}
	return s.guide, nil
}

// TestGetAllSpecies tests the GetAllSpecies endpoint returns all BirdNET labels.
func TestGetAllSpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "all-species-list")

	tests := []struct {
		name           string
		labels         []string
		expectedCount  int
		expectedStatus int
	}{
		{
			name:           "returns all labels",
			labels:         []string{"Turdus migratorius_American Robin", "Cyanocitta cristata_Blue Jay", "Corvus brachyrhynchos_American Crow"},
			expectedCount:  3,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "empty labels",
			labels:         []string{},
			expectedCount:  0,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "nil labels",
			labels:         nil,
			expectedCount:  0,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			settings := &conf.Settings{}
			settings.BirdNET.Labels = tt.labels

			controller := &Controller{
				Echo:     e,
				Group:    e.Group("/api/v2"),
				Settings: settings,
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v2/species/all", http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			err := controller.GetAllSpecies(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, rec.Code)

			var response AllSpeciesResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Equal(t, tt.expectedCount, response.Count)
			assert.Len(t, response.Species, tt.expectedCount)

			if tt.expectedCount > 0 {
				// Verify first species is parsed correctly
				assert.Equal(t, "Turdus migratorius_American Robin", response.Species[0].Label)
				assert.Equal(t, "Turdus migratorius", response.Species[0].ScientificName)
				assert.Equal(t, "American Robin", response.Species[0].CommonName)

				// Verify order is preserved
				assert.Equal(t, "Cyanocitta cristata_Blue Jay", response.Species[1].Label)
				assert.Equal(t, "Cyanocitta cristata", response.Species[1].ScientificName)
				assert.Equal(t, "Blue Jay", response.Species[1].CommonName)
			}
		})
	}
}

// TestClassifyGuideQuality tests the guide quality classification logic.
func TestClassifyGuideQuality(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "guide-quality")

	tests := []struct {
		name        string
		description string
		partial     bool
		expected    GuideQuality
	}{
		{
			name:        "full guide with sections",
			description: "An introduction paragraph.\n\n## Description\nA detailed description.\n\n## Songs and calls\nVarious songs.",
			partial:     false,
			expected:    GuideQualityFull,
		},
		{
			name:        "intro only - no sections",
			description: "A common blackbird found throughout Europe.",
			partial:     false,
			expected:    GuideQualityIntroOnly,
		},
		{
			name:        "stub - empty description",
			description: "",
			partial:     false,
			expected:    GuideQualityStub,
		},
		{
			name:        "stub - partial flag set",
			description: "Some text",
			partial:     true,
			expected:    GuideQualityStub,
		},
		{
			name:        "stub - partial with empty",
			description: "",
			partial:     true,
			expected:    GuideQualityStub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := classifyGuideQuality(tt.description, tt.partial)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// speciesGuideTestSettings returns Settings with the species guide feature enabled,
// which is required for notes CRUD handlers to pass their master feature guard.
func speciesGuideTestSettings() *conf.Settings {
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
	return settings
}

// TestGetSpeciesNotes tests the GetSpeciesNotes endpoint.
func TestGetSpeciesNotes(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "species-notes")

	t.Run("feature disabled returns 503", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/species/Turdus%20merula/notes", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("scientific_name")
		ctx.SetParamValues("Turdus merula")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: &conf.Settings{}}
		err := c.GetSpeciesNotes(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Contains(t, rec.Body.String(), "Species guide feature is disabled")
	})

	t.Run("empty scientific name", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/species//notes", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("scientific_name")
		ctx.SetParamValues("")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: speciesGuideTestSettings()}
		err := c.GetSpeciesNotes(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("returns notes for species", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		mockDS.EXPECT().
			GetSpeciesNotes("Turdus merula").
			Return([]datastore.SpeciesNote{
				{ID: 1, ScientificName: "Turdus merula", Entry: "Seen in garden"},
				{ID: 2, ScientificName: "Turdus merula", Entry: "Singing at dawn"},
			}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/species/Turdus%20merula/notes", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("scientific_name")
		ctx.SetParamValues("Turdus merula")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), DS: mockDS, Settings: speciesGuideTestSettings()}
		err := c.GetSpeciesNotes(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var notes []SpeciesNoteResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &notes))
		assert.Len(t, notes, 2)
		assert.Equal(t, "Seen in garden", notes[0].Entry)
	})
}

// TestCreateSpeciesNote tests the CreateSpeciesNote endpoint.
func TestCreateSpeciesNote(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "species-notes")

	t.Run("feature disabled returns 503", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		body := `{"entry":"Beautiful singer"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/species/Turdus%20merula/notes",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("scientific_name")
		ctx.SetParamValues("Turdus merula")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: &conf.Settings{}}
		err := c.CreateSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Contains(t, rec.Body.String(), "Species guide feature is disabled")
	})

	t.Run("empty entry", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		body := `{"entry":""}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/species/Turdus%20merula/notes",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("scientific_name")
		ctx.SetParamValues("Turdus merula")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: speciesGuideTestSettings()}
		err := c.CreateSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("successful creation", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		mockDS.EXPECT().
			SaveSpeciesNote(mock.AnythingOfType("*datastore.SpeciesNote")).
			Run(func(note *datastore.SpeciesNote) {
				assert.Equal(t, "Turdus merula", note.ScientificName)
				assert.Equal(t, "Beautiful singer", note.Entry)
			}).
			Return(nil)

		body := `{"entry":"Beautiful singer"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/species/Turdus%20merula/notes",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("scientific_name")
		ctx.SetParamValues("Turdus merula")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), DS: mockDS, Settings: speciesGuideTestSettings()}
		err := c.CreateSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, rec.Code)
	})
}

// TestDeleteSpeciesNote tests the DeleteSpeciesNote endpoint.
func TestDeleteSpeciesNote(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "species-notes")

	t.Run("feature disabled returns 503", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		req := httptest.NewRequest(http.MethodDelete, "/api/v2/species/notes/42", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues("42")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: &conf.Settings{}}
		err := c.DeleteSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Contains(t, rec.Body.String(), "Species guide feature is disabled")
	})

	t.Run("empty ID", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		req := httptest.NewRequest(http.MethodDelete, "/api/v2/species/notes/", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues("")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: speciesGuideTestSettings()}
		err := c.DeleteSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("successful deletion", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		mockDS.EXPECT().
			DeleteSpeciesNote("42").
			Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v2/species/notes/42", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues("42")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), DS: mockDS, Settings: speciesGuideTestSettings()}
		err := c.DeleteSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}

// TestUpdateSpeciesNote tests the UpdateSpeciesNote endpoint.
func TestUpdateSpeciesNote(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "species-notes")

	t.Run("feature disabled returns 503", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		body := `{"entry":"updated"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v2/species/notes/42",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues("42")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), Settings: &conf.Settings{}}
		err := c.UpdateSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Contains(t, rec.Body.String(), "Species guide feature is disabled")
	})

	t.Run("successful update", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		mockDS.EXPECT().
			UpdateSpeciesNote("42", "updated entry").
			Return(nil)
		mockDS.EXPECT().
			GetSpeciesNoteByID(uint(42)).
			Return(&datastore.SpeciesNote{
				ID:             42,
				ScientificName: "Turdus merula",
				Entry:          "updated entry",
			}, nil)

		body := `{"entry":"updated entry"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v2/species/notes/42",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues("42")

		c := &Controller{Echo: e, Group: e.Group("/api/v2"), DS: mockDS, Settings: speciesGuideTestSettings()}
		err := c.UpdateSpeciesNote(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var note SpeciesNoteResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &note))
		assert.Equal(t, "updated entry", note.Entry)
	})
}

// TestScoreToExpectedness tests the scoreToExpectedness helper function.
func TestScoreToExpectedness(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "expectedness")

	tests := []struct {
		name     string
		score    float64
		expected Expectedness
	}{
		{
			name:     "high score is expected",
			score:    0.9,
			expected: ExpectednessExpected,
		},
		{
			name:     "moderate score is expected",
			score:    0.6,
			expected: ExpectednessExpected,
		},
		{
			name:     "borderline common/uncommon is uncommon",
			score:    0.4,
			expected: ExpectednessUncommon,
		},
		{
			name:     "low score is rare",
			score:    0.1,
			expected: ExpectednessRare,
		},
		{
			name:     "very low score is unexpected",
			score:    0.01,
			expected: ExpectednessUnexpected,
		},
		{
			name:     "zero score is unexpected",
			score:    0.0,
			expected: ExpectednessUnexpected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := scoreToExpectedness(tt.score)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestComputeCurrentSeason tests the computeCurrentSeason helper function.
func TestComputeCurrentSeason(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "seasonal-context")

	tests := []struct {
		name     string
		latitude float64
		date     time.Time
		expected string
	}{
		{
			name:     "northern hemisphere - spring",
			latitude: 52.0,
			date:     time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC),
			expected: "spring",
		},
		{
			name:     "northern hemisphere - summer",
			latitude: 52.0,
			date:     time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC),
			expected: "summer",
		},
		{
			name:     "northern hemisphere - fall",
			latitude: 52.0,
			date:     time.Date(2024, 10, 15, 12, 0, 0, 0, time.UTC),
			expected: "fall",
		},
		{
			name:     "northern hemisphere - winter",
			latitude: 52.0,
			date:     time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			expected: "winter",
		},
		{
			name:     "southern hemisphere - spring in september",
			latitude: -35.0,
			date:     time.Date(2024, 10, 15, 12, 0, 0, 0, time.UTC),
			expected: "spring",
		},
		{
			name:     "southern hemisphere - summer in january",
			latitude: -35.0,
			date:     time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			expected: "summer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := computeCurrentSeason(tt.latitude, tt.date)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBuildExternalLinks tests the buildExternalLinks helper function.
func TestBuildExternalLinks(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "external-links")

	t.Run("generates links for common name", func(t *testing.T) {
		t.Parallel()
		links := buildExternalLinks("Northern Cardinal", "Passer domesticus")
		require.Len(t, links, 2)

		assert.Equal(t, "All About Birds", links[0].Name)
		assert.Equal(t, "https://www.allaboutbirds.org/guide/Northern_Cardinal", links[0].URL)

		assert.Equal(t, "Xeno-canto", links[1].Name)
		assert.Contains(t, links[1].URL, "xeno-canto.org/species/")
	})

	t.Run("handles apostrophes", func(t *testing.T) {
		t.Parallel()
		links := buildExternalLinks("Cooper's Hawk", "Accipiter cooperii")
		require.Len(t, links, 2)
		assert.Equal(t, "https://www.allaboutbirds.org/guide/Coopers_Hawk", links[0].URL)
	})

	t.Run("returns nil for empty name", func(t *testing.T) {
		t.Parallel()
		links := buildExternalLinks("", "")
		assert.Nil(t, links)
	})

	t.Run("falls back to scientific name for All About Birds when common name is empty", func(t *testing.T) {
		t.Parallel()
		links := buildExternalLinks("", "Turdus migratorius")
		require.Len(t, links, 2)

		assert.Equal(t, "All About Birds", links[0].Name)
		assert.Equal(t, "https://www.allaboutbirds.org/guide/Turdus_migratorius", links[0].URL)

		assert.Equal(t, "Xeno-canto", links[1].Name)
		assert.Equal(t, "https://xeno-canto.org/species/Turdus-migratorius", links[1].URL)
	})
}
