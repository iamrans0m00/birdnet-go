// Seasonal keyword highlighting for species guide text.
// Wraps season-relevant words in <mark> tags for visual emphasis.

const seasonKeywords: Record<string, string[]> = {
  spring: [
    'spring', 'breeding', 'nesting', 'migration', 'courtship', 'mating',
    'territorial', 'nest', 'eggs', 'clutch', 'incubation', 'fledgling',
  ],
  summer: [
    'summer', 'breeding', 'nesting', 'fledgling', 'juvenile', 'molt',
    'territory', 'foraging', 'chicks', 'brood',
  ],
  fall: [
    'fall', 'autumn', 'migration', 'flocking', 'staging', 'southward',
    'departure', 'molt', 'pre-migration', 'passage',
  ],
  winter: [
    'winter', 'wintering', 'overwintering', 'roost', 'flocking',
    'irruption', 'northward', 'arrival', 'hibernation',
  ],
  // Equatorial seasons
  wet1: ['wet', 'rainy', 'monsoon', 'breeding', 'nesting'],
  dry1: ['dry', 'drought', 'migration', 'foraging'],
  wet2: ['wet', 'rainy', 'monsoon', 'breeding', 'nesting'],
  dry2: ['dry', 'drought', 'migration', 'foraging'],
};

/**
 * Wraps season-relevant keywords in `<mark class="season-highlight">` tags.
 * Returns the original text if no season is provided or no keywords match.
 */
export function highlightSeasonKeywords(text: string, currentSeason: string | undefined): string {
  if (!currentSeason || !text) return text;

  if (!Object.hasOwn(seasonKeywords, currentSeason)) return text;
  // eslint-disable-next-line security/detect-object-injection -- validated via Object.hasOwn above
  const keywords = seasonKeywords[currentSeason];

  // Build a single regex that matches any keyword as a whole word (case-insensitive).
  // Keywords are compile-time constants, so dynamic RegExp is safe here.
  // eslint-disable-next-line security/detect-non-literal-regexp
  const pattern = new RegExp(`\\b(${keywords.join('|')})\\b`, 'gi');

  return text.replace(pattern, '<mark class="season-highlight">$1</mark>');
}
