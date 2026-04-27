// Species Selector Type Definitions
//
// For current analytics types, prefer importing from:
// - `frontend/src/lib/desktop/features/analytics/pages/AdvancedAnalytics.svelte` (interface definitions)
// - Use the typed interfaces there for new analytics code instead of the legacy `count` property

/**
 * Branded type for species IDs to prevent mixing with other strings
 * Use createSpeciesId() to create instances from raw strings
 */
export type SpeciesId = string & { __brand: 'SpeciesId' };

/**
 * Creates a SpeciesId from a raw string
 * @param id - Raw string ID
 * @returns Branded SpeciesId
 */
export function createSpeciesId(id: string): SpeciesId {
  return id as SpeciesId;
}

export interface Species {
  id: SpeciesId;
  commonName: string;
  scientificName?: string;
  frequency?: SpeciesFrequency;
  category?: string;
  description?: string;
  imageUrl?: string;
  /**
   * @deprecated For backwards compatibility only
   *
   * Legacy detection count for analytics display. This property is maintained
   * for compatibility with existing components but should not be used in new code.
   *
   * For new analytics features, use the typed interfaces in:
   * `frontend/src/lib/desktop/features/analytics/pages/AdvancedAnalytics.svelte`
   * which provide proper type safety and structure for analytics data.
   */
  count?: number;
}

export type SpeciesFrequency = 'very-common' | 'common' | 'uncommon' | 'rare';

export type SpeciesSelectorSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

export type SpeciesSelectorVariant = 'chip' | 'list' | 'compact';

export interface SpeciesSelectorConfig {
  size: SpeciesSelectorSize;
  variant: SpeciesSelectorVariant;
  maxSelections?: number;
  searchable: boolean;
  categorized: boolean;
  showFrequency: boolean;
}

export interface SpeciesGroup {
  category: string;
  items: Species[];
}

// Guide quality level indicating content richness
export type GuideQuality = 'full' | 'intro_only' | 'stub';

// Species expectedness in the user's area at the current time of year
export type Expectedness = 'expected' | 'uncommon' | 'rare' | 'unexpected';

// External link to a bird identification resource
export interface ExternalLink {
  name: string;
  url: string;
}

// Feature flags indicating which optional guide features are enabled
export interface GuideFeatureFlags {
  notes: boolean;
  enrichments: boolean;
  similar_species: boolean;
}

// Species guide data returned by the /api/v2/species/:name/guide endpoint
export interface SpeciesGuideData {
  scientific_name: string;
  common_name: string;
  description: string;
  conservation_status: string;
  quality: GuideQuality;
  expectedness?: Expectedness;
  current_season?: string;
  external_links?: ExternalLink[];
  features: GuideFeatureFlags;
  source: {
    provider: string;
    url: string;
    license: string;
    license_url: string;
  };
  partial: boolean;
  cached_at: string;
}

// Parsed sections from species guide for comparison
export interface SimilarSpeciesSections {
  description?: string;
  songs_and_calls?: string;
  similar_species?: string[];
}

// Similar species entry returned by /api/v2/species/:name/similar
export interface SimilarSpeciesEntry {
  scientific_name: string;
  common_name: string;
  relationship: 'same_genus' | 'same_family' | 'similar';
  guide_summary?: string;
  sections?: SimilarSpeciesSections;
}

// Response from /api/v2/species/:name/similar
export interface SimilarSpeciesResponse {
  scientific_name: string;
  genus: string;
  similar: SimilarSpeciesEntry[];
}

// Aggregated species summary used by detail modals (dashboard, analytics).
export interface SpeciesData {
  common_name: string;
  scientific_name: string;
  count: number;
  avg_confidence: number | null;
  max_confidence: number | null;
  first_heard: string;
  last_heard: string;
  thumbnail_url?: string;
}

// Species note data returned by /api/v2/species/:name/notes
export interface SpeciesNoteData {
  id: number;
  entry: string;
  created_at: string;
  updated_at: string;
}

// Parsed guide section with optional heading and body text
export interface GuideSection {
  heading: string;
  body: string;
}

/**
 * Parse a guide description that contains `## Section` markdown headers
 * into an array of { heading, body } segments for structured rendering.
 */
export function parseGuideDescription(description: string): GuideSection[] {
  const sections: GuideSection[] = [];
  const parts = description.split(/^## /m);

  for (const part of parts) {
    const trimmed = part.trim();
    if (!trimmed) continue;

    const newlineIdx = trimmed.indexOf('\n');
    if (newlineIdx === -1) {
      if (sections.length === 0 && !description.trimStart().startsWith('## ')) {
        sections.push({ heading: '', body: trimmed });
      } else {
        sections.push({ heading: trimmed, body: '' });
      }
    } else {
      const heading =
        sections.length === 0 && !description.trimStart().startsWith('## ')
          ? ''
          : trimmed.slice(0, newlineIdx).trim();
      const body =
        sections.length === 0 && !description.trimStart().startsWith('## ')
          ? trimmed
          : trimmed.slice(newlineIdx + 1).trim();
      sections.push({ heading, body });
    }
  }

  return sections;
}

// Event types for the species selector
export interface SpeciesSelectorEvents {
  change: { selected: SpeciesId[] };
  add: { species: Species };
  remove: { species: Species };
  search: { query: string };
  clear: Record<string, never>;
}
