# Species Guide Feature Rollout

## Overview

The Species Guide feature provides contextual information about detected bird species, sourced from Wikipedia and eBird. It's designed to help users learn more about birds they detect in their recordings.

### Features

- **Wikipedia Provider**: Fetches species information from Wikipedia's REST API
- **eBird Provider**: Enriches data with eBird taxonomy (requires API key)
- **Two-tier Caching**: Memory cache + database persistence
- **Stale-while-revalidate**: Returns cached data immediately while refreshing in background

## Configuration by Environment

### Development

All features enabled for testing:

```yaml
realtime:
  dashboard:
    speciesGuide:
      enabled: true
      provider: wikipedia
      fallbackPolicy: all
      warmTopN: 10
      preFetchEnabled: true
```

### Staging

Notes disabled to test core functionality:

```yaml
realtime:
  dashboard:
    speciesGuide:
      enabled: true
      provider: wikipedia
      fallbackPolicy: all
      warmTopN: 50
      preFetchEnabled: true
```

### Production

Gradual rollout with monitoring:

```yaml
realtime:
  dashboard:
    speciesGuide:
      enabled: true
      provider: wikipedia
      fallbackPolicy: all
      warmTopN: 0  # Disable warm-up to observe natural cache behavior
      preFetchEnabled: false  # Disable pre-fetch until cache hit rate is established
```

## Monitoring During Rollout

### Key Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `guidecache_hit_ratio` | Cache hit ratio (0-1) | < 0.5 after 24h |
| `guidecache_hits_total` | Cache hits by provider/quality | N/A |
| `guidecache_misses_total` | Cache misses by provider | Spike > 2x baseline |
| `guidecache_wikipedia_duration_seconds` | Wikipedia API latency | > 10s p95 |
| `guidecache_db_operation_duration_seconds` | DB operation latency | > 1s p95 |

### Prometheus Query Examples

```promql
# Cache hit ratio
rate(guidecache_hits_total[5m]) / (rate(guidecache_hits_total[5m]) + rate(guidecache_misses_total[5m]))

# Wikipedia API p95 latency
histogram_quantile(0.95, rate(guidecache_wikipedia_duration_seconds_bucket[5m]))

# Cache size
guidecache_hits_total + guidecache_misses_total
```

## Troubleshooting

### Guide not showing in detection details

1. Check Species Guide is enabled:
   ```bash
   curl http://localhost:8080/api/v2/settings | jq '.realtime.dashboard.speciesGuide.enabled'
   ```

2. Check provider is configured:
   ```bash
   curl http://localhost:8080/api/v2/settings | jq '.realtime.dashboard.speciesGuide.provider'
   ```

3. Check server logs for guide cache initialization:
   ```bash
   grep -i "species guide" /var/log/birdnet.log
   ```

### Notes not saving

1. Verify storage is working:
   ```bash
   curl http://localhost:8080/api/v2/prerequisites
   ```

2. Check database has write permissions

3. Look for auth errors in logs

### Wikipedia errors in logs

1. Check circuit breaker status:
   ```bash
   grep -i "circuit breaker" /var/log/birdnet.log
   ```

2. Rate limiting: Wikipedia limits requests to 1/sec. If hitting limit, pre-fetch or reduce usage.

3. Network issues: Check DNS resolution and connectivity to wikipedia.org

## Rollback Procedure

### Via Settings (No Restart Required)

1. Disable the feature via API:
   ```bash
   curl -X PATCH http://localhost:8080/api/v2/settings \
     -H "Content-Type: application/json" \
     -d '{"realtime":{"dashboard":{"speciesGuide":{"enabled":false}}}}'
   ```

2. Pre-fetch and memory cache will stop immediately
3. Database cache remains for next enable

### Full Rollback (Requires Restart)

1. Set in config.yaml:
   ```yaml
   realtime:
     dashboard:
       speciesGuide:
         enabled: false
   ```

2. Restart the service

## Performance Considerations

- **Memory**: Each cached entry is ~2-10KB. With 10,000 entries, expect ~20-100MB.
- **Database**: Each entry adds ~1-5KB. Monitor table size with:
  ```sql
  SELECT pg_size_pretty(pg_total_relation_size('guide_caches'));
  ```
- **Wikipedia API**: Rate limited to 1 request/second. Pre-fetch helps avoid misses.

## Security

- No PII stored in guide cache
- Wikipedia content is CC BY-SA 4.0 licensed
- eBird data governed by eBird terms of use
