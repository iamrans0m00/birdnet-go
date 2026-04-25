/**
 * Frontend analytics tracking for BirdNET-Go.
 *
 * Provides a simple event tracking system that:
 * - Logs events to console in development mode
 * - Captures as Sentry breadcrumbs for error correlation
 * - Never logs sensitive data (PII, credentials, tokens)
 *
 * Usage:
 * ```typescript
 * import { trackEvent } from '$lib/telemetry/analytics';
 *
 * trackEvent('species_guide_viewed', {
 *   species: 'Turdus merula',
 *   guide_quality: 'full',
 *   provider: 'wikipedia'
 * });
 * ```
 */

import * as Sentry from '@sentry/browser';
import { getLogger } from '$lib/utils/logger';
import { getLocalDateString } from '$lib/utils/date';

const logger = getLogger('analytics');

const ANALYTICS_CATEGORY = 'analytics';

/** Sensitive key patterns to redact from event labels */
const SENSITIVE_KEYS =
  /(token|password|secret|apikey|api_key|authorization|cookie|session|sessionid|email|\bip\b|ip_address)/i;

/**
 * Redact sensitive keys from an object to prevent PII leakage.
 * Recursively handles nested objects and arrays.
 */
function redactSensitive(data: unknown): unknown {
  if (data === null || data === undefined) {
    return data;
  }

  if (Array.isArray(data)) {
    return data.map(item => redactSensitive(item));
  }

  if (typeof data === 'object') {
    return Object.fromEntries(
      Object.entries(data as Record<string, unknown>).map(([key, value]) => {
        if (SENSITIVE_KEYS.test(key)) {
          return [key, '[redacted]'];
        }
        if (typeof value === 'string' && value.length > 500) {
          return [key, value.slice(0, 500) + '...[truncated]'];
        }
        if (typeof value === 'object') {
          return [key, redactSensitive(value)];
        }
        return [key, value];
      })
    );
  }

  if (typeof data === 'string' && data.length > 500) {
    return data.slice(0, 500) + '...[truncated]';
  }

  return data;
}

/**
 * Track an analytics event.
 *
 * @param eventName - The name of the event (e.g., 'species_comparison_opened')
 * @param labels - Optional labels/context for the event (sensitive keys will be redacted)
 */
export function trackEvent(eventName: string, labels?: Record<string, unknown>): void {
  const timestamp = getLocalDateString();
  const safeLabels = labels ? (redactSensitive(labels) as Record<string, unknown>) : {};

  // Log to console in development
  if (import.meta.env.DEV) {
    logger.debug(`[event] ${eventName}`, safeLabels);
  }

  // Capture as Sentry breadcrumb for error correlation
  try {
    Sentry.addBreadcrumb({
      category: ANALYTICS_CATEGORY,
      message: eventName,
      level: 'info',
      data: safeLabels,
      timestamp: new Date(timestamp).getTime(),
    });
  } catch {
    // Sentry not available at runtime
  }
}

/**
 * Analytics event names for type safety
 */
export const AnalyticsEvents = {
  SPECIES_COMPARISON_OPENED: 'species_comparison_opened',
  SPECIES_NOTE_CREATED: 'species_note_created',
  SPECIES_NOTE_UPDATED: 'species_note_updated',
  SPECIES_NOTE_DELETED: 'species_note_deleted',
  SPECIES_GUIDE_VIEWED: 'species_guide_viewed',
} as const;

/**
 * Type for known analytics event names
 */
export type AnalyticsEventName = (typeof AnalyticsEvents)[keyof typeof AnalyticsEvents];
