package analysis

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// initGuideCacheIfNeeded initializes the species guide cache if the feature is enabled.
// Returns nil if the feature is disabled or required dependencies are unavailable.
func initGuideCacheIfNeeded(settings *conf.Settings, ds datastore.Interface) *guideprovider.GuideCache {
	log := GetLogger()

	if !settings.Realtime.Dashboard.SpeciesGuide.Enabled {
		log.Info("species guide feature is disabled")
		return nil
	}

	// Get *gorm.DB from the datastore for the guide cache store.
	concreteDS, ok := ds.(*datastore.DataStore)
	if !ok || concreteDS.DB == nil {
		log.Warn("could not get database connection for guide cache")
		return nil
	}

	store, err := guideprovider.NewGORMGuideStore(concreteDS.DB)
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
