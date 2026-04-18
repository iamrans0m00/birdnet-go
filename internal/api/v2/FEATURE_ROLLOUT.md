# Species Guide Feature Rollout

## Overview

The Species Guide feature provides contextual information about detected bird species, sourced from Wikipedia and eBird. It's designed to help users learn more about birds they detect in their recordings.

### Features

- **Wikipedia Provider**: Fetches species information from Wikipedia's REST API
- **eBird Provider**: Enriches data with eBird taxonomy (requires API key)
- **Two-tier Caching**: Memory cache + database persistence
- **Stale-while-revalidate**: Returns cached data immediately while refreshing in background

## Configuration Key Convention

All Species Guide YAML configuration keys use **all-lowercase** names (no camelCase), consistent with the BirdNET-Go project-wide YAML convention. Examples: `speciesguide`, `fallbackpolicy`, `warmtopn`, `prefetchenabled`. These are new keys introduced with this feature — no existing users have prior camelCase variants in their `config.yaml`.

## Configuration by Environment

### Development

All features enabled for testing:

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      provider: wikipedia
      fallbackpolicy: all
      warmtopn: 10
      prefetchenabled: true
      shownotes: true
      showenrichments: true
      showsimilarspecies: true
```

### Staging

With notes and enrichments enabled for full testing:

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      provider: wikipedia
      fallbackpolicy: all
      warmtopn: 50
      prefetchenabled: true
      shownotes: true
      showenrichments: true
      showsimilarspecies: true
```

### Production

Gradual rollout with monitoring:

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      provider: wikipedia
      fallbackpolicy: all
      warmtopn: 100        # Pre-warm common species on startup
      prefetchenabled: true # Pre-fetch guides for newly detected species
      shownotes: true
      showenrichments: true
      showsimilarspecies: true
```

## Monitoring During Rollout

### Key Metrics

### UI Feature Toggles

Three independent toggles control which guide sections render:

```yaml
shownotes: true             # Show user-authored species notes with CRUD interface
showenrichments: true       # Show season/expectedness badges and external links:
                            #   - All About Birds (Cornell Lab)
                            #   - Xeno-canto (recordings)
showsimilarspecies: true    # Show similar species comparison modal with:
                            #   - Scientific names
                            #   - Side-by-side descriptions
                            #   - Identification tips
```

**Note:** These are independent toggles. You can show notes but hide comparisons, for example.

## Monitoring During Rollout

### Key Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `guidecache_hits_total` | Cache hits by provider/quality | N/A |
| `guidecache_misses_total` | Cache misses by provider | Spike > 2x baseline |
| `guidecache_positive_entry_ratio` | Fraction of cached entries with data (0-1) | < 0.5 after warm-up |
| `guidecache_wikipedia_duration_seconds` | Wikipedia API latency by endpoint | > 10s p95 |
| `guidecache_db_operation_duration_seconds` | DB operation latency | > 1s p95 |

### Prometheus Query Examples

```promql
# Cache hit ratio (request-level)
rate(guidecache_hits_total[5m]) / (rate(guidecache_hits_total[5m]) + rate(guidecache_misses_total[5m]))

# Positive cache entry ratio (storage-level: % of entries with data vs not-found markers)
guidecache_positive_entry_ratio

# Wikipedia API p95 latency
histogram_quantile(0.95, rate(guidecache_wikipedia_duration_seconds_bucket[5m]))

# Total API requests by endpoint and result
guide_cache_wikipedia_requests_total
```

## Troubleshooting

### Guide not showing in detection details

1. Check Species Guide is enabled:
   ```bash
   curl http://localhost:8080/api/v2/settings | jq '.realtime.dashboard.speciesguide.enabled'
   ```

2. Check appropriate toggle is enabled:
   ```bash
   curl http://localhost:8080/api/v2/settings | jq '.realtime.dashboard.speciesguide'
   ```
   Verify `shownotes`, `showenrichments`, and/or `showsimilarspecies` are true

3. Check provider is configured:
   ```bash
   curl http://localhost:8080/api/v2/settings | jq '.realtime.dashboard.speciesguide.provider'
   ```

4. Check server logs for guide cache initialization:
   ```bash
   grep -i "guide" logs/api.log | head -20
   ```

### Notes not saving

1. Verify `shownotes` is enabled:
   ```bash
   curl http://localhost:8080/api/v2/settings | jq '.realtime.dashboard.speciesguide.shownotes'
   ```

2. Check database has write permissions and `guide_caches` table exists

3. Verify authentication (notes are per-user):
   ```bash
   curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v2/auth/status
   ```

4. Look for errors in logs:
   ```bash
   grep -i "error.*note" logs/api.log
   ```

### Wikipedia errors in logs

1. Check circuit breaker status:
   ```bash
   grep -i "circuit breaker" logs/api.log
   ```

2. Rate limiting: BirdNET-Go throttles to 1 request/sec per instance (by design, not required by Wikipedia). Wikipedia itself allows ~200 req/sec per IP. If you see rate limit errors, check:
   - Wikipedia's HTTP status page for outages
   - Circuit breaker duration (opens for 5 minutes on repeated failures)
   - Pre-fetch cache hit ratio (enable warm-up to reduce live lookups)

3. Network issues: Check DNS resolution and connectivity to en.wikipedia.org

## Rollback Procedure

### Via Settings (No Restart Required)

1. Disable the feature via API:
   ```bash
   curl -X PATCH http://localhost:8080/api/v2/settings \
     -H "Content-Type: application/json" \
     -d '{"realtime":{"dashboard":{"speciesguide":{"enabled":false}}}}'
   ```

2. Pre-fetch and memory cache will stop immediately
3. Database cache remains for next enable

4. Or selectively disable UI components:
   ```bash
   # Hide similar species comparisons while keeping guides enabled
   curl -X PATCH http://localhost:8080/api/v2/settings \
     -H "Content-Type: application/json" \
     -d '{"realtime":{"dashboard":{"speciesguide":{"showsimilarspecies":false}}}}'
   ```

### Full Rollback (Requires Restart)

1. Set in config.yaml:
   ```yaml
   realtime:
     dashboard:
       speciesguide:
         enabled: false
   ```

2. Restart the service

## Performance Considerations

- **Memory**: Each cached entry is ~2-10KB. With 10,000 entries, expect ~20-100MB.
- **Database**: Each entry adds ~1-5KB. Monitor table size with:
  ```sql
  SELECT pg_size_pretty(pg_total_relation_size('guide_caches'));
  ```
- **Wikipedia API**: BirdNET-Go enforces **1 request/second** per instance (voluntary throttle to be respectful). Wikipedia's actual limit is ~200 req/sec per IP. Pre-warming cache on startup helps avoid repeated lookups.

## Security

- No PII stored in guide cache
- Wikipedia content is CC BY-SA 4.0 licensed
- eBird data governed by eBird terms of use
