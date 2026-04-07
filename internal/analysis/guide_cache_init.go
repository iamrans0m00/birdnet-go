package analysis

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"gorm.io/gorm"
)

// managerProvider is a narrow interface for datastores that expose a v2 Manager.
// The v2only.Datastore satisfies this via its Manager() method.
type managerProvider interface {
	Manager() v2.Manager
}

// directDBProvider is a narrow interface for datastores that expose a GORM
// connection directly. The legacy DataStore/SQLiteStore satisfy this via
// their embedded GetDB() method.
type directDBProvider interface {
	GetDB() *gorm.DB
}

// extractDB attempts to get a *gorm.DB from a datastore, supporting both
// v2only.Datastore (via Manager().DB()) and legacy stores (via GetDB()).
func extractDB(ds any) *gorm.DB {
	// Try v2only.Datastore path first (Manager().DB()).
	if mp, ok := ds.(managerProvider); ok {
		if mgr := mp.Manager(); mgr != nil {
			return mgr.DB()
		}
	}
	// Fall back to legacy path (GetDB()).
	if dp, ok := ds.(directDBProvider); ok {
		return dp.GetDB()
	}
	return nil
}

// initGuideCacheIfNeeded initializes the species guide cache if the feature is enabled.
// Returns nil if the feature is disabled or required dependencies are unavailable.
// If WarmTopN > 0, it warms the cache with the top detected species after startup.
func initGuideCacheIfNeeded(settings *conf.Settings, ds any, store datastore.Interface, m *metrics.GuideProviderMetrics) *guideprovider.GuideCache {
	log := GetLogger()

	if !settings.Realtime.Dashboard.SpeciesGuide.Enabled {
		log.Info("species guide feature is disabled")
		return nil
	}

	// Get *gorm.DB from the datastore.
	db := extractDB(ds)
	if db == nil {
		log.Warn("could not get database connection for guide cache")
		return nil
	}

	guideStore, err := guideprovider.NewGORMGuideStoreWithMetrics(db, m)
	if err != nil {
		log.Error("failed to initialize guide cache store", logger.Error(err))
		return nil
	}

	cache := guideprovider.NewGuideCache(guideStore, m)

	// Register Wikipedia provider (always available, no API key needed).
	wikiProvider := guideprovider.NewWikipediaGuideProviderWithMetrics(m)
	cache.RegisterProvider(guideprovider.WikipediaProviderName, wikiProvider)

	// Register eBird provider if API key is configured.
	if settings.Realtime.EBird.Enabled && settings.Realtime.EBird.APIKey != "" {
		ebirdClient, clientErr := ebird.NewClient(ebird.Config{
			APIKey: settings.Realtime.EBird.APIKey,
		})
		if clientErr == nil {
			ebirdProvider, provErr := guideprovider.NewEBirdGuideProviderWithMetrics(ebirdClient, m)
			if provErr == nil {
				cache.RegisterProvider(guideprovider.EBirdProviderName, ebirdProvider)
				log.Info("registered eBird guide provider")
			}
		}
	}

	cache.Start()
	log.Info("species guide cache initialized")

	// Warm the cache with top detected species if configured.
	if warmN := settings.Realtime.Dashboard.SpeciesGuide.WarmTopN; warmN > 0 && store != nil {
		warmGuideCacheWithTopSpecies(cache, store, warmN, log)
	}

	return cache
}

// warmGuideCacheWithTopSpecies fetches all detected species and warms the cache
// for the top N (by number of detections).
func warmGuideCacheWithTopSpecies(cache *guideprovider.GuideCache, ds datastore.Interface, topN int, log logger.Logger) {
	notes, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Warn("failed to get detected species for cache warming", logger.Error(err))
		return
	}

	// GetAllDetectedSpecies returns unique species — take up to topN.
	names := make([]string, 0, min(topN, len(notes)))
	for i := range notes {
		if len(names) >= topN {
			break
		}
		if notes[i].ScientificName != "" {
			names = append(names, notes[i].ScientificName)
		}
	}

	if len(names) > 0 {
		log.Info("warming guide cache with detected species",
			logger.Int("requested", topN),
			logger.Int("available", len(names)))
		cache.WarmForSpecies(names)
	}
}
