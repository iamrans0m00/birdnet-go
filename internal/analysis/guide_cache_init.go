package analysis

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
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
func initGuideCacheIfNeeded(settings *conf.Settings, ds any) *guideprovider.GuideCache {
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

	store, err := guideprovider.NewGORMGuideStore(db)
	if err != nil {
		log.Error("failed to initialize guide cache store", logger.Error(err))
		return nil
	}

	cache := guideprovider.NewGuideCache(store)

	// Register Wikipedia provider (always available, no API key needed).
	wikiProvider := guideprovider.NewWikipediaGuideProvider()
	cache.RegisterProvider(guideprovider.WikipediaProviderName, wikiProvider)

	// Register eBird provider if API key is configured.
	if settings.Realtime.EBird.Enabled && settings.Realtime.EBird.APIKey != "" {
		ebirdClient, clientErr := ebird.NewClient(ebird.Config{
			APIKey: settings.Realtime.EBird.APIKey,
		})
		if clientErr == nil {
			ebirdProvider, provErr := guideprovider.NewEBirdGuideProvider(ebirdClient)
			if provErr == nil {
				cache.RegisterProvider(guideprovider.EBirdProviderName, ebirdProvider)
				log.Info("registered eBird guide provider")
			}
		}
	}

	cache.Start()
	log.Info("species guide cache initialized")
	return cache
}
