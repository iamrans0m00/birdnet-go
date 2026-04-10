import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import SpeciesComparison from './SpeciesComparison.svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/utils/urlHelpers', () => ({
  buildAppUrl: vi.fn((path: string) => `http://localhost:8080${path}`),
}));

vi.mock('$lib/utils/logger', () => ({
  loggers: {
    ui: {
      error: vi.fn(),
    },
  },
}));

vi.mock('$lib/telemetry/analytics', () => ({
  trackEvent: vi.fn(),
  AnalyticsEvents: {
    SPECIES_COMPARISON_OPENED: 'species_comparison_opened',
  },
}));

vi.mock('$lib/types/species', () => ({
  parseGuideDescription: vi.fn((description: string) => {
    if (!description) return [];
    const sections = description.split('## ');
    return sections.slice(1).map(section => {
      const [heading, ...bodyParts] = section.split('\n');
      return {
        heading: heading.trim(),
        body: bodyParts.join('\n').trim(),
      };
    });
  }),
}));

describe('SpeciesComparison', () => {
  const comparisonTest = createComponentTestFactory(SpeciesComparison);

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('renders header with title and close button', () => {
    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    expect(screen.getByText('analytics.species.similar.title')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /common.close/i })).toBeInTheDocument();
  });

  it('displays loading state initially', () => {
    global.fetch = vi.fn(() => new Promise(() => {})) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    expect(screen.getByText('analytics.species.similar.loading')).toBeInTheDocument();
  });

  it('displays empty state when no similar species', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ similar: [] }),
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText('analytics.species.similar.empty')).toBeInTheDocument();
    });
  });

  it('displays similar species list when data is returned', async () => {
    const mockSimilarSpecies = [
      {
        scientific_name: 'Turdus philomelos',
        common_name: 'Song Thrush',
        guide_summary: 'A similar thrush species',
      },
      {
        scientific_name: 'Turdus migratorius',
        common_name: 'American Robin',
        guide_summary: 'Another thrush',
      },
    ];

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ similar: mockSimilarSpecies }),
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getAllByText('Song Thrush').length).toBeGreaterThan(0);
      expect(screen.getByText('Turdus philomelos')).toBeInTheDocument();
    });
  });

  it('displays column header with focal species', async () => {
    global.fetch = vi.fn((url: string) => {
      if (url.includes('/api/v2/species/') && url.includes('/similar')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ similar: [] }),
        });
      }
      // Return valid response for other URLs (guide endpoint)
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText('American Robin')).toBeInTheDocument();
      expect(screen.getByText('Turdus migratorius')).toBeInTheDocument();
    });
  });

  it('calls onclose when close button is clicked', async () => {
    const onClose = vi.fn();

    global.fetch = vi.fn((url: string) => {
      if (url.includes('/api/v2/species/') && url.includes('/similar')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ similar: [] }),
        });
      }
      // Return valid response for other URLs (guide endpoint)
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: onClose,
      },
    });

    await waitFor(async () => {
      const closeButton = screen.getByRole('button', { name: /common.close/i });
      await fireEvent.click(closeButton);
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  it('highlights selected species in the list', async () => {
    const mockSimilarSpecies = [
      {
        scientific_name: 'Turdus philomelos',
        common_name: 'Song Thrush',
        guide_summary: 'A similar thrush species',
      },
    ];

    global.fetch = vi.fn((url: string) => {
      if (url.includes('/api/v2/species/') && url.includes('/similar')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ similar: mockSimilarSpecies }),
        });
      }
      // Return valid response for other URLs (guide endpoint)
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({}),
      });
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      const items = screen.getAllByText('Song Thrush');
      const item = items[0].closest('button');
      expect(item).toHaveClass('active');
    });
  });

  it('shows loading indicator when fetching guide for selected species', async () => {
    const mockSimilarSpecies = [
      {
        scientific_name: 'Turdus philomelos',
        common_name: 'Song Thrush',
        guide_summary: 'A similar thrush species',
      },
    ];

    let resolveGuide: (value: unknown) => void;
    const guidePromise = new Promise(resolve => {
      resolveGuide = resolve;
    });

    global.fetch = vi.fn((url: string) => {
      if (url.includes('/api/v2/species/') && url.includes('/similar')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ similar: mockSimilarSpecies }),
        });
      }
      return guidePromise as unknown as Response;
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText('Song Thrush')).toBeInTheDocument();
    });

    const items = screen.getAllByText('Song Thrush');
    const item = items[0].closest('button');
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    await fireEvent.click(item!);

    expect(screen.getByText('analytics.species.guide.loading')).toBeInTheDocument();

    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    resolveGuide!({
      ok: true,
      json: () =>
        Promise.resolve({
          common_name: 'Song Thrush',
          description: '## Description\nA beautiful songbird',
        }),
    });
  });

  it('displays guide content when selected species guide is loaded', async () => {
    const mockSimilarSpecies = [
      {
        scientific_name: 'Turdus philomelos',
        common_name: 'Song Thrush',
        guide_summary: 'A similar thrush species',
      },
    ];

    global.fetch = vi.fn((url: string) => {
      if (url.includes('/api/v2/species/') && url.includes('/similar')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ similar: mockSimilarSpecies }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            common_name: 'Song Thrush',
            description:
              '## Description\nA beautiful songbird\n\n## Songs and calls\nMelodious warble',
          }),
      });
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText('Song Thrush')).toBeInTheDocument();
    });

    const allItems = screen.getAllByText('Song Thrush');
    const item = allItems[0].closest('button');
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    await fireEvent.click(item!);

    await waitFor(() => {
      expect(screen.getAllByText('Song Thrush').length).toBeGreaterThan(0);
      expect(screen.getByText('Description')).toBeInTheDocument();
    });
  });

  it('handles API error gracefully', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('Network error')) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText('analytics.species.similar.empty')).toBeInTheDocument();
    });
  });

  it('displays guide summary in species list items', async () => {
    const mockSimilarSpecies = [
      {
        scientific_name: 'Turdus philomelos',
        common_name: 'Song Thrush',
        guide_summary: 'A small to medium-sized bird',
      },
    ];

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ similar: mockSimilarSpecies }),
    }) as unknown as typeof fetch;

    comparisonTest.render({
      props: {
        scientificName: 'Turdus migratorius',
        commonName: 'American Robin',
        onclose: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText('A small to medium-sized bird')).toBeInTheDocument();
    });
  });
});
