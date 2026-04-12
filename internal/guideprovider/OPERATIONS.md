# Species Guide Provider - Operations Guide

## Overview

The Species Guide Provider caches bird species information from Wikipedia and eBird APIs, providing offline-capable contextual information for users. This document covers operational concerns: monitoring, scaling, troubleshooting, and maintenance.

## Configuration

### Enable/Disable

Species guides are **disabled by default**. Enable in config:

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true                  # Enable the feature
      provider: wikipedia            # Primary provider: wikipedia | auto
      fallbackpolicy: all            # Fallback: none (primary only) | all (try all providers)
      warmtopn: 50                   # Pre-warm N most common species on startup
      prefetchenabled: true          # Pre-fetch guides for detected species in background
```

### Environment-Specific Recommendations

**Development:**
- All features enabled for compatibility testing
- `prefetchenabled: false` to reduce API load

**Staging:**
- `enabled: true` for functional testing
- `warmtopn: 50` to cache common species
- Monitor metrics for provider reliability

**Production:**
- `enabled: true` (rolled out gradually to users)
- `warmtopn: 100-200` to pre-warm cache
- `fallbackpolicy: all` for resilience to provider outages

## Cache Architecture

### Two-Tier Caching

1. **Memory Cache** (sync.Map): In-process, fast, lost on restart
2. **Database Cache** (GORM): Persistent, survives restarts, slower

### TTL Strategy

- **Positive entries** (guide found): 7 days
- **Negative entries** (guide not found): 30 minutes
- **Database retention**: 30 days
- **Refresh interval**: Every 2 hours

### Cache Entries

A cached entry includes:
- Scientific name + provider + locale (composite key)
- Description, common name, conservation status (~10KB typical)
- Source URL, license info for attribution
- Timestamp for TTL evaluation

## Disk & Database Management

### Storage Limits

- **Description field**: Truncated to 10,000 characters (Wikipedia → Summary fallback)
- **Total per entry**: ~2-5 KB
- **Database table**: No hard limit; automatic cleanup prevents unbounded growth

### Automatic Cleanup

The cache runs a cleanup sweep every 2 hours during TTL refresh:

1. Identifies all entries beyond retention period (30 days old)
2. Deletes rows from `guide_caches` table
3. Logs count of deleted entries at DEBUG level

**Example cleanup log:**
```
Cleaned up old guide cache entries deleted=42 provider=wikipedia
```

### Monitoring Cleanup

Check cleanup metrics:
```sql
-- Estimate current cache size
SELECT 
  COUNT(*) as entry_count,
  ROUND(SUM(LENGTH(description)) / 1024.0 / 1024.0, 2) as description_size_mb
FROM guide_caches;

-- See cache age distribution
SELECT 
  cached_at,
  COUNT(*) as entries
FROM guide_caches
GROUP BY DATE(cached_at)
ORDER BY cached_at DESC
LIMIT 10;
```

## Monitoring & Metrics

### Prometheus Metrics (Experimental)

The `GuideProviderMetrics` implementation provides:

```
# Cache effectiveness
guidecache_hits_total{provider="wikipedia",quality="full"} 1234
guidecache_misses_total{provider="wikipedia"} 56
guidecache_population_ratio{provider="wikipedia"} 0.95  # 95% positive entries

# API performance
wikipedia_api_latency_seconds{endpoint="/extract",result="success"} 0.15
wikipedia_api_requests_total{endpoint="/extract",result="success"} 890

# Database operations
db_operation_duration_seconds{operation="save",quantile="0.95"} 0.002
db_operations_total{operation="get",status="success"} 5678
```

### Manual Health Check

```bash
# Test guide retrieval
curl "http://localhost:8080/api/v2/species/Turdus%20merula/guide"

# Test with specific locale
curl "http://localhost:8080/api/v2/species/Turdus%20merula/guide?locale=de"
```

Response indicates:
- `"quality": "full"` — Rich content (identification + behavior sections)
- `"quality": "stub"` — Only intro paragraph
- `"quality": "not_found"` — Negative cache (species not in provider)

### Alerts to Set

| Condition | Severity | Action |
|-----------|----------|--------|
| All providers unavailable (5+ min) | High | Page on-call; check Wikipedia/eBird status |
| Cache hit ratio < 80% | Medium | Increase `warmtopn` or `prefetchenabled` |
| DB operation latency p95 > 100ms | Medium | Check database load, consider table indexes |
| Memory usage spike | Medium | Restart to clear in-memory cache |

## Troubleshooting

### Guides Not Loading

**Symptom:** API returns `404` or `503` for all guides

**Check:**
1. Is feature enabled? `curl http://localhost:8080/settings | grep speciesguide`
2. Is provider responding? `curl https://en.wikipedia.org/api/rest_v1/page/summary/Turdus_merula`
3. Check logs: `grep -i "guideprovider" logs/api.log`

**Resolution:**
- If provider down: Client sees 503; wait for recovery (usually < 1 hour)
- If disabled: Set `enabled: true` in config, restart server
- If database issue: Check schema; run migrations: `internal/datastore migrate`

### Cache Growing Too Large

**Symptom:** Database size exceeds 1 GB; slow queries

**Check:**
```sql
SELECT COUNT(*) FROM guide_caches; -- Should be < 50,000
SELECT MAX(LENGTH(description)) FROM guide_caches; -- Should be <= 10,000
```

**Resolution:**
1. Manual cleanup (if scheduled cleanup broken):
```sql
DELETE FROM guide_caches 
WHERE cached_at < DATE_SUB(NOW(), INTERVAL 30 DAY)
LIMIT 10000;  -- Rate-limited deletes to avoid lock contention
```
2. Check if cleanup goroutine is running: search for "Stopped guide cache refresh routine" in logs
3. Restart cache if hung: Restart application (graceful shutdown waits for cleanup)

### High API Latency

**Symptom:** Guide requests take > 500ms

**Check:**
1. Is it cache hit or provider fetch?
   - Cache hit: Query should be < 10ms (memory) or < 50ms (database)
   - Provider fetch: Wikipedia REST API typically 200-500ms
2. Is provider rate-limited? Check `wikipedia_api_requests_total` metric
3. Database slow? Run: `EXPLAIN SELECT ... FROM guide_caches WHERE ...`

**Resolution:**
- Increase `warmtopn` to pre-cache more entries
- Enable `prefetchenabled` for background warming during detections
- Add database index on `(provider_name, cached_at)` if missing

### Locale Support Issues

**Symptom:** Non-English guides not loading for locale requests

**Check:**
1. Is locale valid? (e.g., `de`, `fr`, `es` — must be Wikipedia language code, not BCP47)
2. Does species have article in that language?
   - Test: `https://[locale].wikipedia.org/api/rest_v1/page/summary/[species]`
   - May return 404 for rare species not translated

**Resolution:**
- Frontend should gracefully fall back to English if locale fails
- Check `fallbackpolicy: all` is set to retry English on locale failure

## Performance Tuning

### Pre-warming Strategy

```yaml
warmtopn: 200  # Pre-warm most common 200 species
```

Typical species list:
- House Sparrow (Passer domesticus)
- European Starling (Sturnus vulgaris)
- American Robin (Turdus migratorius)
- etc.

**Trade-off:** Pre-warming takes ~2-5 minutes on startup; saves 200 first-time API calls.

### Prefetch Strategy

```yaml
prefetchenabled: true  # Async fetch guides for detected species
```

Runs in background as detections occur; reduces latency for subsequent guide views.

**Trade-off:** Adds 1-2 background requests per detection; reduces user-facing latency.

### Database Indexing

Recommended index for cache lookup performance:
```sql
CREATE INDEX idx_guidecache_lookup 
  ON guide_caches(provider_name, scientific_name, locale);
```

## Scaling Considerations

### Single Instance

- **Cache size**: ~50,000 entries typical
- **Memory**: ~150-200 MB (in-memory cache)
- **Database**: ~500 MB GORM datastore

### Multi-Instance Deployment

Each instance maintains **independent in-memory cache** but shares **single database**.

**Consequences:**
- First request to new instance: Database fetch (slower)
- Second+ requests: Memory cache (fast)
- After 2 hours: Background refresh syncs all instances

**Optimization:**
- Increase `warmtopn` on each instance startup to pre-populate memory
- Monitor per-instance cache hit ratio to detect cold starts

### Load Balancing

No special coordination needed; cache is read-heavy (GET requests only).

## API Rate Limiting

### Wikipedia API

- **Limit**: ~200 requests/second per IP
- **Configured timeout**: 10 seconds per request
- **Fallback**: If timeout, returns `ErrAllProvidersUnavailable` (503)

### eBird API

- **Limit**: Depends on API key tier (typically 100/min for free tier)
- **Configured timeout**: 10 seconds per request

**Monitoring:**
```
wikipedia_api_requests_total{result="timeout"}  # Indicates hitting limits
```

## Disaster Recovery

### Cache Corruption

**Symptom:** Negative cache spread; empty descriptions returned

**Resolution:**
1. Stop application
2. Clear cache table: `DELETE FROM guide_caches;`
3. Clear memory cache: Restart application
4. Restart with `warmtopn: 100+` to repopulate

### Database Loss

If `guide_caches` table deleted:
1. Run migration: `internal/datastore migrate`
2. Cache will rebuild automatically as requests come in
3. No data loss; just temporary latency spike during rebuild

### Provider API Permanently Down

If Wikipedia/eBird API becomes unavailable:
1. Set `enabled: false` in config
2. Application continues; API endpoints return 503
3. Feature gracefully degrades; no app crash

## Maintenance Tasks

### Monthly

- [ ] Check cache size: `SELECT COUNT(*) FROM guide_caches;`
- [ ] Review metrics: cache hit ratio, API latency p95
- [ ] Check logs for errors: `grep ERROR logs/api.log | grep guideprovider`

### Quarterly

- [ ] Analyze cache age: which entries are stale? (Consider adjusting TTL)
- [ ] Review provider API status pages
- [ ] Test locale functionality for major supported languages

### Yearly

- [ ] Audit database table size and consider archive/cleanup strategy
- [ ] Review Wikipedia/eBird API terms of service for changes
- [ ] Update documentation if operational patterns change
