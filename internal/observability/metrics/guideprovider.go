// Package metrics provides guide provider metrics for observability.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// GuideProviderMetrics contains Prometheus metrics for guide provider operations.
type GuideProviderMetrics struct {
	registry *prometheus.Registry

	// Cache metrics
	cacheHitsTotal      *prometheus.CounterVec
	cacheMissesTotal    *prometheus.CounterVec
	cachePositiveRatio  prometheus.Gauge

	// Wikipedia API metrics
	wikipediaAPILatency       *prometheus.HistogramVec
	wikipediaAPIRequestsTotal *prometheus.CounterVec

	// Database operation metrics
	dbOperationDuration *prometheus.HistogramVec
	dbOperationsTotal   *prometheus.CounterVec

	collectors []prometheus.Collector
}

// NewGuideProviderMetrics creates and registers new guide provider metrics.
func NewGuideProviderMetrics(registry *prometheus.Registry) (*GuideProviderMetrics, error) {
	m := &GuideProviderMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics.
func (m *GuideProviderMetrics) initMetrics() error {
	// Cache hit/miss counters
	m.cacheHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guidecache_hits_total",
			Help: "Total number of guide cache hits",
		},
		[]string{"provider", "quality"}, // provider: wikipedia, ebird; quality: full, stub, intro_only
	)

	m.cacheMissesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guidecache_misses_total",
			Help: "Total number of guide cache misses",
		},
		[]string{"provider"},
	)

	m.cachePositiveRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "guidecache_positive_entry_ratio",
		Help: "Fraction of stored guide cache entries containing real data vs not-found markers (0.0 to 1.0). Not a request-level hit ratio.",
	})

	// Wikipedia API latency histogram
	m.wikipediaAPILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "guidecache_wikipedia_duration_seconds",
			Help:    "Time taken for Wikipedia API requests",
			Buckets: prometheus.ExponentialBuckets(BucketStart100ms, BucketFactor2, BucketCount12), // 100ms to ~400s
		},
		[]string{"endpoint", "result"}, // endpoint: search, extract, links; result: success, not_found, rate_limited, error
	)

	// Wikipedia API request counter
	m.wikipediaAPIRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guidecache_wikipedia_requests_total",
			Help: "Total number of Wikipedia API requests",
		},
		[]string{"endpoint", "result"},
	)

	// Database operation duration
	m.dbOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "guidecache_db_operation_duration_seconds",
			Help:    "Time taken for guide cache DB operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount12), // 1ms to ~4s
		},
		[]string{"operation"}, // operation: get, save, get_all
	)

	// Database operation counter
	m.dbOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guidecache_db_operations_total",
			Help: "Total number of guide cache DB operations",
		},
		[]string{"operation", "status"}, // operation: get, save, get_all; status: success, error
	)

	// Initialize collectors slice
	m.collectors = []prometheus.Collector{
		m.cacheHitsTotal,
		m.cacheMissesTotal,
		m.cachePositiveRatio,
		m.wikipediaAPILatency,
		m.wikipediaAPIRequestsTotal,
		m.dbOperationDuration,
		m.dbOperationsTotal,
	}

	return nil
}

// Describe implements the Collector interface.
func (m *GuideProviderMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range m.collectors {
		collector.Describe(ch)
	}
}

// Collect implements the Collector interface.
func (m *GuideProviderMetrics) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range m.collectors {
		collector.Collect(ch)
	}
}

// RecordCacheHit records a cache hit with provider and quality labels.
func (m *GuideProviderMetrics) RecordCacheHit(provider, quality string) {
	m.cacheHitsTotal.WithLabelValues(provider, quality).Inc()
}

// RecordCacheMiss records a cache miss with provider label.
func (m *GuideProviderMetrics) RecordCacheMiss(provider string) {
	m.cacheMissesTotal.WithLabelValues(provider).Inc()
}

// RecordWikipediaAPICall records a Wikipedia API call with latency and result status.
func (m *GuideProviderMetrics) RecordWikipediaAPICall(endpoint, result string, duration float64) {
	m.wikipediaAPILatency.WithLabelValues(endpoint, result).Observe(duration)
	m.wikipediaAPIRequestsTotal.WithLabelValues(endpoint, result).Inc()
}

// RecordDBOperation records a database operation with duration and status.
func (m *GuideProviderMetrics) RecordDBOperation(operation, status string, duration float64) {
	m.dbOperationsTotal.WithLabelValues(operation, status).Inc()
	m.dbOperationDuration.WithLabelValues(operation).Observe(duration)
}

// UpdateCachePopulationRatio updates the gauge tracking what fraction of stored
// cache entries contain real guide data (positive) vs not-found markers (negative).
func (m *GuideProviderMetrics) UpdateCachePopulationRatio(positive, negative float64) {
	if positive+negative > 0 {
		m.cachePositiveRatio.Set(positive / (positive + negative))
	}
}
