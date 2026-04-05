package guideprovider

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for repeated string literals.
const (
	testSpeciesMerula      = "Turdus merula"
	testSpeciesParus       = "Parus major"
	testSpeciesCorvus      = "Corvus corax"
	testCommonBlackbird    = "Common Blackbird"
	testWarmBirdCommonName = "Test Bird"
	testWarmBirdDesc       = "A test bird."
)

// mockGuideProvider is a test double for GuideProvider.
type mockGuideProvider struct {
	fetchFunc func(ctx context.Context, scientificName string) (SpeciesGuide, error)
}

func (m *mockGuideProvider) Fetch(ctx context.Context, scientificName string, _ FetchOptions) (SpeciesGuide, error) {
	return m.fetchFunc(ctx, scientificName)
}

// mockGuideStore is an in-memory test double for GuideStore.
type mockGuideStore struct {
	entries map[string]*GuideCacheEntry
}

func newMockGuideStore() *mockGuideStore {
	return &mockGuideStore{entries: make(map[string]*GuideCacheEntry)}
}

func (s *mockGuideStore) GetGuideCache(_ context.Context, scientificName, providerName string) (*GuideCacheEntry, error) {
	key := providerName + ":" + scientificName
	entry, ok := s.entries[key]
	if !ok {
		return nil, nil //nolint:nilnil // record not found is not an error; nil entry is the expected signal
	}
	return entry, nil
}

func (s *mockGuideStore) SaveGuideCache(_ context.Context, entry *GuideCacheEntry) error {
	key := entry.ProviderName + ":" + entry.ScientificName
	s.entries[key] = entry
	return nil
}

func (s *mockGuideStore) GetAllGuideCaches(_ context.Context, providerName string) ([]GuideCacheEntry, error) {
	var result []GuideCacheEntry
	for _, entry := range s.entries {
		if entry.ProviderName == providerName {
			result = append(result, *entry)
		}
	}
	return result, nil
}

func TestSpeciesGuide_IsNegativeEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		guide    SpeciesGuide
		expected bool
	}{
		{
			name:     "positive entry",
			guide:    SpeciesGuide{SourceProvider: WikipediaProviderName},
			expected: false,
		},
		{
			name:     "negative entry",
			guide:    SpeciesGuide{SourceProvider: negativeEntryMarker},
			expected: true,
		},
		{
			name:     "empty provider",
			guide:    SpeciesGuide{SourceProvider: ""},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.guide.IsNegativeEntry())
		})
	}
}

func TestIsCacheEntryStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cachedAt   time.Time
		isNegative bool
		expected   bool
	}{
		{
			name:       "fresh positive entry",
			cachedAt:   time.Now().Add(-1 * time.Hour),
			isNegative: false,
			expected:   false,
		},
		{
			name:       "stale positive entry",
			cachedAt:   time.Now().Add(-8 * 24 * time.Hour),
			isNegative: false,
			expected:   true,
		},
		{
			name:       "fresh negative entry",
			cachedAt:   time.Now().Add(-5 * time.Minute),
			isNegative: true,
			expected:   false,
		},
		{
			name:       "stale negative entry",
			cachedAt:   time.Now().Add(-31 * time.Minute),
			isNegative: true,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isCacheEntryStale(tt.cachedAt, tt.isNegative))
		})
	}
}

func TestMergeGuides(t *testing.T) {
	t.Parallel()

	primary := SpeciesGuide{
		ScientificName: testSpeciesMerula,
		CommonName:     testCommonBlackbird,
		Description:    "A species of true thrush.",
		SourceProvider: WikipediaProviderName,
	}

	secondary := SpeciesGuide{
		ScientificName:     testSpeciesMerula,
		CommonName:         "Eurasian Blackbird",
		ConservationStatus: "Least Concern",
		SourceProvider:     EBirdProviderName,
	}

	result := mergeGuides(&primary, &secondary)

	// Primary fields take precedence
	assert.Equal(t, testCommonBlackbird, result.CommonName)
	assert.Equal(t, "A species of true thrush.", result.Description)

	// Secondary fills gaps
	assert.Equal(t, "Least Concern", result.ConservationStatus)

	// Partial is false because description is populated
	assert.False(t, result.Partial)
}

func TestMergeGuides_PrimaryEmpty(t *testing.T) {
	t.Parallel()

	primary := SpeciesGuide{
		ScientificName: testSpeciesMerula,
	}

	secondary := SpeciesGuide{
		ScientificName: testSpeciesMerula,
		CommonName:     testCommonBlackbird,
		Description:    "A bird.",
	}

	result := mergeGuides(&primary, &secondary)
	assert.Equal(t, testCommonBlackbird, result.CommonName)
	assert.Equal(t, "A bird.", result.Description)
	assert.False(t, result.Partial)
}

func TestDbEntryToGuide(t *testing.T) {
	t.Parallel()

	now := time.Now()
	entry := &GuideCacheEntry{
		ScientificName:     testSpeciesMerula,
		CommonName:         testCommonBlackbird,
		Description:        "A species.",
		ConservationStatus: "Least Concern",
		SourceProvider:     WikipediaProviderName,
		SourceURL:          "https://en.wikipedia.org/wiki/Common_blackbird",
		LicenseName:        "CC BY-SA 4.0",
		LicenseURL:         "https://creativecommons.org/licenses/by-sa/4.0/",
		CachedAt:           now,
	}

	guide := dbEntryToGuide(entry)
	assert.Equal(t, testSpeciesMerula, guide.ScientificName)
	assert.Equal(t, testCommonBlackbird, guide.CommonName)
	assert.Equal(t, "A species.", guide.Description)
	assert.Equal(t, WikipediaProviderName, guide.SourceProvider)
	assert.Equal(t, now, guide.CachedAt)
	assert.False(t, guide.Partial) // Description is non-empty
}

func TestGuideCache_GetFromMemory(t *testing.T) {
	t.Parallel()

	cache := NewGuideCache(nil)
	defer cache.Close()

	// Pre-populate memory cache
	guide := &SpeciesGuide{
		ScientificName: testSpeciesMerula,
		CommonName:     testCommonBlackbird,
		Description:    "A species.",
		SourceProvider: WikipediaProviderName,
		CachedAt:       time.Now(),
	}
	cache.dataMap.Store(testSpeciesMerula, guide)

	result, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, testCommonBlackbird, result.CommonName)
}

func TestGuideCache_NegativeMemoryCacheHit(t *testing.T) {
	t.Parallel()

	cache := NewGuideCache(nil)
	defer cache.Close()

	// Pre-populate with negative entry
	negative := &SpeciesGuide{
		ScientificName: "Unknown species",
		SourceProvider: negativeEntryMarker,
		CachedAt:       time.Now(),
	}
	cache.dataMap.Store("Unknown species", negative)

	_, err := cache.Get(t.Context(), "Unknown species", FetchOptions{})
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestGuideCache_FetchFromProvider(t *testing.T) {
	t.Parallel()

	store := newMockGuideStore()
	cache := NewGuideCache(store)
	defer cache.Close()

	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, scientificName string) (SpeciesGuide, error) {
			if scientificName == testSpeciesMerula {
				return SpeciesGuide{
					ScientificName: testSpeciesMerula,
					CommonName:     testCommonBlackbird,
					Description:    "A species of true thrush.",
					SourceProvider: WikipediaProviderName,
				}, nil
			}
			return SpeciesGuide{}, ErrGuideNotFound
		},
	}
	cache.RegisterProvider(WikipediaProviderName, provider)

	// First fetch should go to the provider
	result, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, testCommonBlackbird, result.CommonName)
	assert.Equal(t, "A species of true thrush.", result.Description)

	// Verify it was cached in memory
	cached, ok := cache.dataMap.Load(testSpeciesMerula)
	assert.True(t, ok)
	assert.Equal(t, testCommonBlackbird, cached.(*SpeciesGuide).CommonName)

	// Verify it was saved to the store
	entry, err := store.GetGuideCache(t.Context(), testSpeciesMerula, WikipediaProviderName)
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "A species of true thrush.", entry.Description)
}

func TestGuideCache_ProviderNotFound(t *testing.T) {
	t.Parallel()

	store := newMockGuideStore()
	cache := NewGuideCache(store)
	defer cache.Close()

	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			return SpeciesGuide{}, ErrGuideNotFound
		},
	}
	cache.RegisterProvider(WikipediaProviderName, provider)

	_, err := cache.Get(t.Context(), "Nonexistent species", FetchOptions{})
	require.ErrorIs(t, err, ErrGuideNotFound)

	// Verify negative entry was cached
	cached, ok := cache.dataMap.Load("Nonexistent species")
	assert.True(t, ok)
	assert.True(t, cached.(*SpeciesGuide).IsNegativeEntry())
}

func TestGuideCacheEntry_TableName(t *testing.T) {
	t.Parallel()
	entry := GuideCacheEntry{}
	assert.Equal(t, "guide_caches", entry.TableName())
}

func TestGuideCache_WarmForSpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "cache-warming")

	synctest.Test(t, func(t *testing.T) {
		store := newMockGuideStore()
		cache := NewGuideCache(store)

		fetchCount := 0
		provider := &mockGuideProvider{
			fetchFunc: func(_ context.Context, name string) (SpeciesGuide, error) {
				fetchCount++
				return SpeciesGuide{
					ScientificName: name,
					CommonName:     testWarmBirdCommonName,
					Description:    testWarmBirdDesc,
					SourceProvider: WikipediaProviderName,
				}, nil
			},
		}
		cache.RegisterProvider(WikipediaProviderName, provider)
		cache.Start()
		defer cache.Close()

		species := []string{testSpeciesMerula, testSpeciesParus, testSpeciesCorvus}
		cache.WarmForSpecies(species)

		// Wait for warm-up goroutine to complete (refresh goroutine blocks on fake ticker).
		synctest.Wait()

		// All species should be in memory cache now
		for _, name := range species {
			_, ok := cache.dataMap.Load(name)
			assert.True(t, ok, "expected %s to be cached", name)
		}
	})
}

func TestGuideCache_WarmForSpecies_SkipsExisting(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "cache-warming")

	synctest.Test(t, func(t *testing.T) {
		store := newMockGuideStore()
		cache := NewGuideCache(store)

		fetchCount := 0
		provider := &mockGuideProvider{
			fetchFunc: func(_ context.Context, name string) (SpeciesGuide, error) {
				fetchCount++
				return SpeciesGuide{
					ScientificName: name,
					CommonName:     testWarmBirdCommonName,
					Description:    testWarmBirdDesc,
					SourceProvider: WikipediaProviderName,
				}, nil
			},
		}
		cache.RegisterProvider(WikipediaProviderName, provider)
		cache.Start()
		defer cache.Close()

		// Pre-populate one species in memory
		cache.dataMap.Store(testSpeciesMerula, &SpeciesGuide{ScientificName: testSpeciesMerula})

		species := []string{testSpeciesMerula, testSpeciesParus}
		cache.WarmForSpecies(species)

		// Wait for warm-up goroutine to complete (refresh goroutine blocks on fake ticker).
		synctest.Wait()

		// Only Parus major should have been fetched (Turdus merula was already cached)
		assert.Equal(t, 1, fetchCount, "should only fetch uncached species")
	})
}

func TestGuideCache_PreFetch(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "prefetch")

	synctest.Test(t, func(t *testing.T) {
		store := newMockGuideStore()
		cache := NewGuideCache(store)

		provider := &mockGuideProvider{
			fetchFunc: func(_ context.Context, name string) (SpeciesGuide, error) {
				return SpeciesGuide{
					ScientificName: name,
					CommonName:     testWarmBirdCommonName,
					Description:    testWarmBirdDesc,
					SourceProvider: WikipediaProviderName,
				}, nil
			},
		}
		cache.RegisterProvider(WikipediaProviderName, provider)
		cache.Start()
		defer cache.Close()

		// PreFetch should be non-blocking
		cache.PreFetch(testSpeciesMerula)

		// Wait for prefetch goroutine to complete (refresh goroutine blocks on fake ticker).
		synctest.Wait()

		// Species should now be in memory cache
		_, ok := cache.dataMap.Load(testSpeciesMerula)
		assert.True(t, ok, "expected %s to be cached after PreFetch", testSpeciesMerula)
	})
}

func TestGuideCache_PreFetch_SkipsExisting(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "prefetch")

	synctest.Test(t, func(t *testing.T) {
		store := newMockGuideStore()
		cache := NewGuideCache(store)

		fetchCount := 0
		provider := &mockGuideProvider{
			fetchFunc: func(_ context.Context, name string) (SpeciesGuide, error) {
				fetchCount++
				return SpeciesGuide{
					ScientificName: name,
					SourceProvider: WikipediaProviderName,
				}, nil
			},
		}
		cache.RegisterProvider(WikipediaProviderName, provider)
		cache.Start()
		defer cache.Close()

		// Pre-populate
		cache.dataMap.Store(testSpeciesMerula, &SpeciesGuide{ScientificName: testSpeciesMerula})

		// PreFetch returns immediately without spawning a goroutine when already cached.
		cache.PreFetch(testSpeciesMerula)
		synctest.Wait()

		assert.Equal(t, 0, fetchCount, "should not fetch already-cached species")
	})
}

func TestGuideCache_WarmForSpecies_Empty(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "cache-warming")

	cache := NewGuideCache(nil)
	// Should not panic with empty or nil list
	cache.WarmForSpecies(nil)
	cache.WarmForSpecies([]string{})
}
