package guideprovider

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// GORMGuideStore implements GuideStore using a GORM database connection.
type GORMGuideStore struct {
	db      *gorm.DB
	metrics GuideCacheMetrics
}

// NewGORMGuideStore creates a new GORMGuideStore and runs auto-migration.
func NewGORMGuideStore(db *gorm.DB) (*GORMGuideStore, error) {
	return NewGORMGuideStoreWithMetrics(db, nil)
}

// NewGORMGuideStoreWithMetrics creates a new GORMGuideStore with metrics and runs auto-migration.
func NewGORMGuideStoreWithMetrics(db *gorm.DB, metrics GuideCacheMetrics) (*GORMGuideStore, error) {
	if db == nil {
		return nil, errors.Newf("guide store database is nil").
			Component("guideprovider").
			Category(errors.CategoryConfiguration).
			Build()
	}
	if err := db.AutoMigrate(&GuideCacheEntry{}); err != nil {
		return nil, errors.Newf("failed to migrate guide_caches table: %w", err).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	return &GORMGuideStore{db: db, metrics: metrics}, nil
}

// GetGuideCache retrieves a guide cache entry by scientific name, provider, and locale.
func (s *GORMGuideStore) GetGuideCache(ctx context.Context, scientificName, providerName, locale string) (*GuideCacheEntry, error) {
	start := time.Now()
	if locale == "" {
		locale = "en"
	}
	var entry GuideCacheEntry
	// Silent: First() logs ErrRecordNotFound at warn level under the stdlib
	// gorm logger, but "not found" is a normal cache miss for us (return
	// nil, nil). Production loggers (datastore.GormLogger, logger.GormLoggerAdapter)
	// already filter ErrRecordNotFound, so this is mostly defense in depth
	// for callers that pass a *gorm.DB without a custom logger (e.g. tests
	// using bare gorm.Open). Use s.db.Logger.LogMode so the configured
	// logger is preserved instead of being replaced by gormlogger.Default.
	// Write paths intentionally keep the configured logger so real GORM
	// errors remain visible.
	err := s.db.WithContext(ctx).
		Session(&gorm.Session{Logger: s.db.Logger.LogMode(gormlogger.Silent)}).
		Where("scientific_name = ? AND provider_name = ? AND locale = ?", scientificName, providerName, locale).
		First(&entry).Error

	status := DBResultSuccess
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = DBResultNotFound
			s.recordDBMetric(DBOperationQueryGuideCaches, status, start)
			return nil, nil //nolint:nilnil // record not found is not an error; nil entry is the expected signal
		}
		s.recordDBError(DBOperationQueryGuideCaches, err, start)
		return nil, errors.Newf("GetGuideCache provider=%s species=%s locale=%s: %w", providerName, scientificName, locale, err).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	s.recordDBMetric(DBOperationQueryGuideCaches, status, start)
	return &entry, nil
}

func (s *GORMGuideStore) recordDBMetric(operation, status string, start time.Time) {
	if s.metrics != nil {
		s.metrics.RecordDBOperation(operation, status, time.Since(start).Seconds())
	}
}

// recordDBError records an error-path DB operation metric using the
// error_type-labelled counter via RecordDBError, classifying the underlying
// error so dashboards can distinguish cancellations/timeouts from real DB faults.
func (s *GORMGuideStore) recordDBError(operation string, err error, start time.Time) {
	if s.metrics != nil {
		s.metrics.RecordDBError(operation, classifyDBError(err), time.Since(start).Seconds())
	}
}

// classifyDBError maps a raw DB error to a low-cardinality error_type label.
func classifyDBError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return DBErrorTypeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return DBErrorTypeDeadline
	default:
		return DBErrorTypeDatabase
	}
}

// SaveGuideCache saves or updates a guide cache entry (upsert).
func (s *GORMGuideStore) SaveGuideCache(ctx context.Context, entry *GuideCacheEntry) error {
	if entry == nil {
		return errors.Newf("guide cache entry cannot be nil").
			Component("guideprovider").
			Category(errors.CategoryValidation).
			Build()
	}
	start := time.Now()
	// Normalize empty locale to "en" so the unique key matches GetGuideCache lookups,
	// which apply the same normalization. Without this, an entry saved with locale=""
	// would never be retrieved by GetGuideCache(..., "") (which queries locale="en").
	if entry.Locale == "" {
		entry.Locale = "en"
	}
	err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider_name"}, {Name: "scientific_name"}, {Name: "locale"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"source_provider", "common_name", "description",
				"conservation_status", "source_url", "license_name",
				"license_url", "similar_species", "cached_at",
			}),
		}).
		Create(entry).Error

	status := DBResultSuccess
	if err != nil {
		s.recordDBError(DBOperationInsertGuideCaches, err, start)
		return errors.Newf("SaveGuideCache provider=%s species=%s locale=%s: %w", entry.ProviderName, entry.ScientificName, entry.Locale, err).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	s.recordDBMetric(DBOperationInsertGuideCaches, status, start)
	return nil
}

// GetAllGuideCaches retrieves guide cache entries for a specific provider,
// filtering out entries cached before notBefore to bound memory at startup.
func (s *GORMGuideStore) GetAllGuideCaches(ctx context.Context, providerName string, notBefore time.Time) ([]GuideCacheEntry, error) {
	start := time.Now()
	var entries []GuideCacheEntry
	// No silent session here: Find() returns nil error + empty slice when no
	// rows match (unlike First(), which returns ErrRecordNotFound). There is
	// no "not found" noise to suppress, and silencing would hide slow-query
	// warnings from the configured logger — useful signal for this scan,
	// which can iterate over many rows at startup and during refresh.
	query := s.db.WithContext(ctx).
		Where("provider_name = ?", providerName)
	if !notBefore.IsZero() {
		query = query.Where("cached_at >= ?", notBefore)
	}
	err := query.Find(&entries).Error

	status := DBResultSuccess
	if err != nil {
		s.recordDBError(DBOperationQueryGuideCaches, err, start)
		getLogger().Warn("Failed to query guide caches",
			logger.String("provider", providerName),
			logger.Error(err))
		return nil, errors.Newf("GetAllGuideCaches provider=%s: %w", providerName, err).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	s.recordDBMetric(DBOperationQueryGuideCaches, status, start)
	return entries, nil
}

// DeleteStaleGuideCaches deletes cache entries older than the specified time.
// Used for database cleanup to prevent unbounded storage growth.
func (s *GORMGuideStore) DeleteStaleGuideCaches(ctx context.Context, providerName string, beforeTime time.Time) (int64, error) {
	start := time.Now()
	result := s.db.WithContext(ctx).
		Where("provider_name = ? AND cached_at < ?", providerName, beforeTime).
		Delete(&GuideCacheEntry{})

	status := DBResultSuccess
	if result.Error != nil {
		s.recordDBError(DBOperationDeleteGuideCaches, result.Error, start)
		getLogger().Warn("Failed to delete stale guide caches",
			logger.String("provider", providerName),
			logger.Error(result.Error))
		return 0, errors.Newf("DeleteStaleGuideCaches provider=%s: %w", providerName, result.Error).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	s.recordDBMetric(DBOperationDeleteGuideCaches, status, start)
	return result.RowsAffected, nil
}
