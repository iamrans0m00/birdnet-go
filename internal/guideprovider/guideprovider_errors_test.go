package guideprovider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestGuideCache_ProviderTimeout tests behavior when a provider exceeds timeout.
func TestGuideCache_ProviderTimeout(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	// Mock provider that simulates timeout
	provider := &mockGuideProvider{
		fetchFunc: func(ctx context.Context, _ string) (SpeciesGuide, error) {
			// Simulate a long-running operation that gets cancelled
			<-ctx.Done()
			return SpeciesGuide{}, context.DeadlineExceeded
		},
	}

	// Set up cache with timeout provider
	store := newMockGuideStore()
	cache := NewGuideCache(store, nil)
	cache.RegisterProvider(WikipediaProviderName, provider)
	cache.Start()
	defer cache.Close()

	// Request with short timeout should fail gracefully
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	guide, err := cache.Get(ctx, testSpeciesMerula, FetchOptions{})

	// Should fail but not panic
	assert.Nil(t, guide)
	assert.Error(t, err)
}

// TestGuideCache_TransientFailureNoNegativeCache tests that transient errors don't get negative-cached.
func TestGuideCache_TransientFailureNoNegativeCache(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	// Mock provider that returns transient error (not 404)
	callCount := 0
	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			callCount++
			if callCount == 1 {
				// First call: transient error (simulate network issue)
				return SpeciesGuide{}, errors.Newf("network timeout").Build()
			}
			// Second call: success
			return SpeciesGuide{
				ScientificName: testSpeciesMerula,
				CommonName:     testCommonBlackbird,
				Description:    "A common European blackbird.",
				SourceProvider: WikipediaProviderName,
			}, nil
		},
	}

	store := newMockGuideStore()
	cache := NewGuideCache(store, nil)
	cache.RegisterProvider(WikipediaProviderName, provider)
	cache.Start()
	defer cache.Close()

	// First call fails with transient error
	guide, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	assert.Nil(t, guide)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrGuideNotFound), "Should NOT be ErrGuideNotFound for transient errors")

	// Second call should retry successfully (not use negative cache)
	guide, err = cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	assert.NotNil(t, guide)
	require.NoError(t, err)
	assert.Equal(t, testSpeciesMerula, guide.ScientificName)
	assert.Equal(t, 2, callCount, "Provider should be called twice (not short-circuited by negative cache)")
}

// TestGuideCache_NegativeCachePreventsFutureErrors tests that negative cache hits prevent retries.
func TestGuideCache_NegativeCachePreventsFutureErrors(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	callCount := 0
	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			callCount++
			return SpeciesGuide{}, ErrGuideNotFound
		},
	}

	store := newMockGuideStore()
	cache := NewGuideCache(store, nil)
	cache.RegisterProvider(WikipediaProviderName, provider)
	cache.Start()
	defer cache.Close()

	// First call: not found
	guide, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	assert.Nil(t, guide)
	require.ErrorIs(t, err, ErrGuideNotFound)
	assert.Equal(t, 1, callCount)

	// Second call: should use negative cache (not call provider again)
	guide, err = cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	assert.Nil(t, guide)
	require.ErrorIs(t, err, ErrGuideNotFound)
	assert.Equal(t, 1, callCount, "Provider should NOT be called again (negative cache should be used)")
}

// TestGuideCache_DescriptionTruncation tests that large descriptions are truncated.
func TestGuideCache_DescriptionTruncation(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "storage-limits")

	// Create a description that exceeds max length
	largeDesc := string(make([]byte, maxRichDescriptionLength+1000))
	for i := range largeDesc {
		largeDesc = largeDesc[:i] + "x"
	}

	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			return SpeciesGuide{
				ScientificName: testSpeciesMerula,
				CommonName:     testCommonBlackbird,
				Description:    largeDesc[:maxRichDescriptionLength+1000], // Intentionally oversized
				SourceProvider: WikipediaProviderName,
			}, nil
		},
	}

	store := newMockGuideStore()
	cache := NewGuideCache(store, nil)
	cache.RegisterProvider(WikipediaProviderName, provider)
	cache.Start()
	defer cache.Close()

	guide, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, guide)

	// Check that description stored in DB is truncated
	entry, err := store.GetGuideCache(t.Context(), testSpeciesMerula, WikipediaProviderName, "en")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.LessOrEqual(t, len(entry.Description), maxRichDescriptionLength, "Database entry should have truncated description")
}

// TestGuideCache_DeleteStaleEntries tests database cleanup of old entries.
func TestGuideCache_DeleteStaleEntries(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "cache-eviction")

	store := newMockGuideStore()

	// Add some old entries
	oldTime := time.Now().Add(-40 * 24 * time.Hour)   // 40 days old (beyond retention)
	recentTime := time.Now().Add(-5 * 24 * time.Hour) // 5 days old (within retention)

	require.NoError(t, store.SaveGuideCache(t.Context(), &GuideCacheEntry{
		ProviderName:   WikipediaProviderName,
		ScientificName: "Old Species 1",
		Locale:         "en",
		CachedAt:       oldTime,
	}))
	require.NoError(t, store.SaveGuideCache(t.Context(), &GuideCacheEntry{
		ProviderName:   WikipediaProviderName,
		ScientificName: "Old Species 2",
		Locale:         "en",
		CachedAt:       oldTime,
	}))
	require.NoError(t, store.SaveGuideCache(t.Context(), &GuideCacheEntry{
		ProviderName:   WikipediaProviderName,
		ScientificName: "Recent Species",
		Locale:         "en",
		CachedAt:       recentTime,
	}))

	// Verify entries exist
	allBefore, err := store.GetAllGuideCaches(t.Context(), WikipediaProviderName, time.Time{})
	require.NoError(t, err)
	assert.Len(t, allBefore, 3, "Should have 3 entries before cleanup")

	// Delete old entries
	cutoffTime := time.Now().Add(-dbRetentionPeriod)
	deleted, err := store.DeleteStaleGuideCaches(t.Context(), WikipediaProviderName, cutoffTime)

	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted, "Should delete 2 old entries")

	// Verify only recent entry remains
	allAfter, err := store.GetAllGuideCaches(t.Context(), WikipediaProviderName, time.Time{})
	require.NoError(t, err)
	assert.Len(t, allAfter, 1, "Should have 1 entry after cleanup")
	assert.Equal(t, "Recent Species", allAfter[0].ScientificName, "Recent entry should remain")
}

// TestTruncateDescription tests the description truncation utility.
func TestTruncateDescription(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "storage-limits")

	tests := []struct {
		name              string
		input             string
		maxLength         int
		expectedMaxLength int
	}{
		{
			name:              "short description unchanged",
			input:             "Short description",
			maxLength:         maxRichDescriptionLength,
			expectedMaxLength: 17,
		},
		{
			name:              "exact length unchanged",
			input:             string(make([]byte, maxRichDescriptionLength)),
			maxLength:         maxRichDescriptionLength,
			expectedMaxLength: maxRichDescriptionLength,
		},
		{
			name:              "oversized truncated",
			input:             string(make([]byte, maxRichDescriptionLength+1000)),
			maxLength:         maxRichDescriptionLength,
			expectedMaxLength: maxRichDescriptionLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateDescription(tt.input)
			assert.LessOrEqual(t, len(result), tt.expectedMaxLength)
		})
	}
}

// TestGuideCache_FallbackProviderSucceeds tests that fallback provider result is used when primary fails.
func TestGuideCache_FallbackProviderSucceeds(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "fallback")

	primaryCalled := false
	fallbackCalled := false

	// Primary provider always fails with transient error
	primaryProvider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			primaryCalled = true
			return SpeciesGuide{}, errors.Newf("primary provider down").Build()
		},
	}

	// Fallback provider succeeds with eBird data
	fallbackProvider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			fallbackCalled = true
			return SpeciesGuide{
				ScientificName: testSpeciesMerula,
				CommonName:     testCommonBlackbird,
				Description:    "eBird data for blackbird",
				SourceProvider: EBirdProviderName,
			}, nil
		},
	}

	store := newMockGuideStore()
	cache := NewGuideCache(store, nil)
	cache.RegisterProvider(WikipediaProviderName, primaryProvider)
	cache.RegisterProvider(EBirdProviderName, fallbackProvider)
	cache.Start()
	defer cache.Close()

	// Request should succeed using fallback data
	guide, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, guide)
	assert.Equal(t, testSpeciesMerula, guide.ScientificName)
	assert.Equal(t, "eBird data for blackbird", guide.Description)
	assert.True(t, primaryCalled, "Primary should have been tried")
	assert.True(t, fallbackCalled, "Fallback should have been tried after primary failed")
}

// TestGuideCache_MergesFallbackResults tests that primary and fallback results are merged.
func TestGuideCache_MergesFallbackResults(t *testing.T) {
	t.Parallel()
	t.Attr("component", "guideprovider")
	t.Attr("type", "unit")
	t.Attr("feature", "fallback")

	// Primary provider provides description
	primaryProvider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			return SpeciesGuide{
				ScientificName: testSpeciesMerula,
				CommonName:     testCommonBlackbird,
				Description:    "Wikipedia: A beautiful black songbird",
				SourceProvider: WikipediaProviderName,
			}, nil
		},
	}

	// Fallback provider provides conservation status (missing in primary)
	fallbackProvider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			return SpeciesGuide{
				ScientificName:     testSpeciesMerula,
				CommonName:         testCommonBlackbird,
				ConservationStatus: testConservationStatusLC,
				SourceProvider:     EBirdProviderName,
			}, nil
		},
	}

	store := newMockGuideStore()
	cache := NewGuideCache(store, nil)
	cache.RegisterProvider(WikipediaProviderName, primaryProvider)
	cache.RegisterProvider(EBirdProviderName, fallbackProvider)
	cache.Start()
	defer cache.Close()

	guide, err := cache.Get(t.Context(), testSpeciesMerula, FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, guide)

	// Merged result should have both description and conservation status
	assert.Equal(t, "Wikipedia: A beautiful black songbird", guide.Description)
	assert.Equal(t, testConservationStatusLC, guide.ConservationStatus)
}
