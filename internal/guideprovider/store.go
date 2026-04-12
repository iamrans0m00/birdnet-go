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
	err := s.db.WithContext(ctx).
		Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).
		Where("scientific_name = ? AND provider_name = ? AND locale = ?", scientificName, providerName, locale).
		First(&entry).Error

	status := DBResultSuccess
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = DBResultNotFound
			s.recordDBMetric("get", status, start)
			return nil, nil //nolint:nilnil // record not found is not an error; nil entry is the expected signal
		}
		status = DBResultError
		s.recordDBMetric("get", status, start)
		return nil, err
	}
	s.recordDBMetric("get", status, start)
	return &entry, nil
}

func (s *GORMGuideStore) recordDBMetric(operation, status string, start time.Time) {
	if s.metrics != nil {
		s.metrics.RecordDBOperation(operation, status, time.Since(start).Seconds())
	}
}

// SaveGuideCache saves or updates a guide cache entry (upsert).
func (s *GORMGuideStore) SaveGuideCache(ctx context.Context, entry *GuideCacheEntry) error {
	start := time.Now()
	err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider_name"}, {Name: "scientific_name"}, {Name: "locale"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"source_provider", "common_name", "description",
				"conservation_status", "source_url", "license_name",
				"license_url", "cached_at",
			}),
		}).
		Create(entry).Error

	status := DBResultSuccess
	if err != nil {
		status = DBResultError
	}
	s.recordDBMetric("save", status, start)
	return err
}

// GetAllGuideCaches retrieves guide cache entries for a specific provider,
// filtering out entries cached before notBefore to bound memory at startup.
func (s *GORMGuideStore) GetAllGuideCaches(ctx context.Context, providerName string, notBefore time.Time) ([]GuideCacheEntry, error) {
	start := time.Now()
	var entries []GuideCacheEntry
	query := s.db.WithContext(ctx).
		Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).
		Where("provider_name = ?", providerName)
	if !notBefore.IsZero() {
		query = query.Where("cached_at >= ?", notBefore)
	}
	err := query.Find(&entries).Error

	status := DBResultSuccess
	if err != nil {
		status = DBResultError
		getLogger().Warn("Failed to query guide caches",
			logger.String("provider", providerName),
			logger.Any("error", err))
	}
	s.recordDBMetric("get_all", status, start)
	return entries, err
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
		status = DBResultError
		getLogger().Warn("Failed to delete stale guide caches",
			logger.String("provider", providerName),
			logger.Any("error", result.Error))
	}
	s.recordDBMetric("delete", status, start)
	return result.RowsAffected, result.Error
}
