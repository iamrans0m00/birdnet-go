import { describe, it, expect } from 'vitest';
import { highlightSeasonKeywords } from './seasonHighlight';

describe('highlightSeasonKeywords', () => {
  it('returns original text when no season provided', () => {
    expect(highlightSeasonKeywords('breeding birds', undefined)).toBe('breeding birds');
  });

  it('returns original text when empty string', () => {
    expect(highlightSeasonKeywords('', 'spring')).toBe('');
  });

  it('highlights spring keywords', () => {
    const result = highlightSeasonKeywords('During breeding season, nesting begins.', 'spring');
    expect(result).toContain('<mark class="season-highlight">breeding</mark>');
    expect(result).toContain('<mark class="season-highlight">nesting</mark>');
  });

  it('highlights winter keywords', () => {
    const result = highlightSeasonKeywords('The bird is commonly seen wintering in marshes.', 'winter');
    expect(result).toContain('<mark class="season-highlight">wintering</mark>');
  });

  it('is case-insensitive', () => {
    const result = highlightSeasonKeywords('Migration patterns vary.', 'fall');
    expect(result).toContain('<mark class="season-highlight">Migration</mark>');
  });

  it('does not highlight partial words', () => {
    const result = highlightSeasonKeywords('The springboard is ready.', 'spring');
    expect(result).not.toContain('<mark');
  });

  it('handles unknown season gracefully', () => {
    const result = highlightSeasonKeywords('Some text about breeding.', 'unknown_season');
    expect(result).toBe('Some text about breeding.');
  });
});
