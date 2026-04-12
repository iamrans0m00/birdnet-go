<script lang="ts">
  import { X, ChevronDown, ChevronRight, Music, Bird, ListOrdered } from '@lucide/svelte';
  import { t, getLocale } from '$lib/i18n';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import type { SimilarSpeciesEntry, SpeciesGuideData } from '$lib/types/species';
  import { parseGuideDescription } from '$lib/types/species';
  import { loggers } from '$lib/utils/logger';
  import { trackEvent, AnalyticsEvents } from '$lib/telemetry/analytics';

  const logger = loggers.ui;

  interface Props {
    scientificName: string;
    commonName: string;
    onclose: () => void;
  }

  let { scientificName, commonName, onclose }: Props = $props();

  let similarSpecies = $state<SimilarSpeciesEntry[]>([]);
  let isLoading = $state(true);
  let focalGuide = $state<SpeciesGuideData | null>(null);
  let focalSections = $state<ReturnType<typeof parseGuideDescription>>([]);
  let selectedSimilarIndex = $state<number>(0);
  let isLoadingSimilarGuide = $state(false);
  let similarGuideSections = $state<ReturnType<typeof parseGuideDescription>>([]);

  // Collapsible section states - Description expanded by default, others collapsed
  let descriptionOpen = $state(true);
  let songsOpen = $state(false);
  let similarOpen = $state(false);

  async function fetchFocalSpeciesGuide(signal?: AbortSignal) {
    try {
      const locale = getLocale();
      const localeParam = locale && locale !== 'en' ? `?locale=${locale}` : '';
      const encodedName = encodeURIComponent(scientificName);
      const url = buildAppUrl(`/api/v2/species/${encodedName}/guide${localeParam}`);
      const response = await fetch(url, { signal });
      if (signal?.aborted) return;
      if (response.ok) {
        const data = await response.json();
        if (signal?.aborted) return;
        focalGuide = data;
        if (focalGuide?.description) {
          focalSections = parseGuideDescription(focalGuide.description);
        }
      }
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.error('Failed to fetch focal species guide', err);
    }
  }

  async function fetchSimilarSpecies(signal?: AbortSignal) {
    isLoading = true;
    try {
      const locale = getLocale();
      const localeParam = locale && locale !== 'en' ? `?locale=${locale}` : '';
      const encodedName = encodeURIComponent(scientificName);
      const url = buildAppUrl(`/api/v2/species/${encodedName}/similar${localeParam}`);
      const response = await fetch(url, { signal });
      if (signal?.aborted) return;
      if (response.ok) {
        const data = await response.json();
        if (signal?.aborted) return;
        similarSpecies = data.similar ?? [];
        trackEvent(AnalyticsEvents.SPECIES_COMPARISON_OPENED, {
          focal_species: scientificName,
          comparison_count: similarSpecies.length,
        });
      }
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.error('Failed to fetch similar species', err);
    } finally {
      isLoading = false;
    }
    // Auto-select first species after list-loading state clears.
    // Fire-and-forget: isLoadingSimilarGuide tracks the guide fetch independently.
    if (similarSpecies.length > 0) {
      void selectSimilar(0);
    }
  }

  async function selectSimilar(index: number) {
    selectedSimilarIndex = index;
    // Reset collapsible states when switching
    descriptionOpen = true;
    songsOpen = false;
    similarOpen = false;
    // Fetch guide for the selected similar species
    if (index >= 0 && index < similarSpecies.length) {
      isLoadingSimilarGuide = true;
      similarGuideSections = [];
      try {
        const entry = similarSpecies[index];
        const encoded = encodeURIComponent(entry.scientific_name);
        const locale = getLocale();
        const localeParam = locale && locale !== 'en' ? `?locale=${locale}` : '';
        const url = buildAppUrl(`/api/v2/species/${encoded}/guide${localeParam}`);
        const response = await fetch(url);
        if (response.ok) {
          const data = await response.json();
          if (data.description) {
            similarGuideSections = parseGuideDescription(data.description);
          }
        }
      } catch (err) {
        logger.error('Failed to fetch similar species guide', err);
      } finally {
        isLoadingSimilarGuide = false;
      }
    }
  }

  $effect(() => {
    const controller = new AbortController();
    void Promise.all([
      fetchSimilarSpecies(controller.signal),
      fetchFocalSpeciesGuide(controller.signal),
    ]);
    return () => controller.abort();
  });

  // Helper to extract specific section from focal guide
  function getSectionContent(
    sections: ReturnType<typeof parseGuideDescription>,
    heading: string
  ): string {
    const section = sections.find(s => s.heading?.toLowerCase() === heading.toLowerCase());
    return section?.body ?? '';
  }
</script>

<div class="species-comparison">
  <div class="comparison-header">
    <h3 class="text-sm font-semibold">
      {t('analytics.species.similar.title')}
    </h3>
    <button class="comparison-close" onclick={onclose} aria-label={t('common.close')}>
      <X class="h-4 w-4" />
    </button>
  </div>

  {#if isLoading}
    <div class="flex items-center gap-2 p-4 text-sm opacity-60">
      <div
        class="animate-spin h-4 w-4 border-2 border-[var(--color-primary)] border-t-transparent rounded-full"
      ></div>
      <span>{t('analytics.species.similar.loading')}</span>
    </div>
  {:else if similarSpecies.length === 0}
    <div class="species-row">
      <button class="species-card focal">
        <span class="species-common">{commonName}</span>
        <span class="species-scientific">{scientificName}</span>
      </button>
    </div>
    <p class="p-4 text-sm opacity-50">{t('analytics.species.similar.empty')}</p>
  {:else}
    <!-- Single row of species cards -->
    <div class="species-row">
      <!-- Focal species card -->
      <button
        class="species-card focal"
        onclick={() => {
          selectedSimilarIndex = -1;
        }}
      >
        <span class="species-common">{commonName}</span>
        <span class="species-scientific">{scientificName}</span>
      </button>

      <!-- Divider -->
      <div class="vs-divider">
        <span>vs</span>
      </div>

      <!-- Similar species cards -->
      {#each similarSpecies as entry, i (entry.scientific_name)}
        <button
          class="species-card"
          class:selected={selectedSimilarIndex === i}
          onclick={() => selectSimilar(i)}
        >
          <span class="species-common">{entry.common_name}</span>
          <span class="species-scientific">{entry.scientific_name}</span>
          {#if entry.relationship}
            <span class="species-relationship">{entry.relationship.replace('_', ' ')}</span>
          {/if}
          {#if entry.guide_summary}
            <span class="species-summary">{entry.guide_summary}</span>
          {/if}
        </button>
      {/each}
    </div>

    <!-- Comparison panel -->
    {#if selectedSimilarIndex >= 0 && selectedSimilarIndex < similarSpecies.length}
      {@const similarEntry = similarSpecies[selectedSimilarIndex]}
      {@const similarSections = similarEntry.sections ?? null}

      <div class="comparison-panel">
        <h4 class="panel-title">
          {commonName} vs {similarEntry.common_name}
        </h4>

        {#if isLoadingSimilarGuide}
          <div class="flex items-center gap-2 p-4 text-sm opacity-60">
            <div
              class="animate-spin h-4 w-4 border-2 border-[var(--color-primary)] border-t-transparent rounded-full"
            ></div>
            <span>{t('analytics.species.guide.loading')}</span>
          </div>
        {:else if similarGuideSections.length > 0}
          <div class="similar-guide-sections">
            {#each similarGuideSections as section}
              <div class="guide-section">
                <h5 class="guide-section-heading">{section.heading}</h5>
                <p class="guide-section-body">{section.body}</p>
              </div>
            {/each}
          </div>
        {/if}

        <!-- Description Section - Expanded by default -->
        <div class="section">
          <button class="section-header" onclick={() => (descriptionOpen = !descriptionOpen)}>
            {#if descriptionOpen}
              <ChevronDown class="h-4 w-4" />
            {:else}
              <ChevronRight class="h-4 w-4" />
            {/if}
            <Bird class="h-4 w-4 text-blue-500" />
            <span>{t('analytics.species.guide.description') || 'Description'}</span>
          </button>
          {#if descriptionOpen}
            <div class="section-content">
              <div class="comparison-row">
                <div class="comparison-side focal">
                  <span class="side-label">{commonName}</span>
                  <p class="side-content">
                    {#if focalSections.length > 0}
                      {getSectionContent(focalSections, 'Description') ||
                        getSectionContent(focalSections, '') ||
                        focalGuide?.description?.substring(0, 300) ||
                        'No description available'}
                    {:else}
                      {focalGuide?.description?.substring(0, 300) || 'No description available'}
                    {/if}
                  </p>
                </div>
                <div class="comparison-side">
                  <span class="side-label">{similarEntry.common_name}</span>
                  <p class="side-content">
                    {similarSections?.description ||
                      similarEntry.guide_summary ||
                      'No description available'}
                  </p>
                </div>
              </div>
            </div>
          {/if}
        </div>

        <!-- Songs and Calls Section - Collapsed by default -->
        <div class="section">
          <button class="section-header" onclick={() => (songsOpen = !songsOpen)}>
            {#if songsOpen}
              <ChevronDown class="h-4 w-4" />
            {:else}
              <ChevronRight class="h-4 w-4" />
            {/if}
            <Music class="h-4 w-4 text-green-500" />
            <span>{t('analytics.species.guide.songs') || 'Songs and calls'}</span>
          </button>
          {#if songsOpen}
            <div class="section-content">
              <div class="comparison-row">
                <div class="comparison-side focal">
                  <span class="side-label">{commonName}</span>
                  <p class="side-content">
                    {getSectionContent(focalSections, 'Songs and calls') ||
                      getSectionContent(focalSections, 'Song and calls') ||
                      getSectionContent(focalSections, 'Vocalisation') ||
                      'No songs/calls info available'}
                  </p>
                </div>
                <div class="comparison-side">
                  <span class="side-label">{similarEntry.common_name}</span>
                  <p class="side-content">
                    {similarSections?.songs_and_calls || 'No songs/calls info available'}
                  </p>
                </div>
              </div>
            </div>
          {/if}
        </div>

        <!-- Similar Species Section - Collapsed by default -->
        <div class="section">
          <button class="section-header" onclick={() => (similarOpen = !similarOpen)}>
            {#if similarOpen}
              <ChevronDown class="h-4 w-4" />
            {:else}
              <ChevronRight class="h-4 w-4" />
            {/if}
            <ListOrdered class="h-4 w-4 text-orange-500" />
            <span>{t('analytics.species.guide.similar') || 'Similar species'}</span>
          </button>
          {#if similarOpen}
            <div class="section-content">
              <div class="comparison-row">
                <div class="comparison-side focal">
                  <span class="side-label">{commonName}</span>
                  <p class="side-content">
                    {#if focalSections.length > 0}
                      {getSectionContent(focalSections, 'Similar species') ||
                        'No similar species listed'}
                    {:else}
                      No similar species listed
                    {/if}
                  </p>
                </div>
                <div class="comparison-side">
                  <span class="side-label">{similarEntry.common_name}</span>
                  <p class="side-content">
                    {#if similarSections?.similar_species && similarSections.similar_species.length > 0}
                      {similarSections.similar_species.join(', ')}
                    {:else}
                      No similar species listed
                    {/if}
                  </p>
                </div>
              </div>
            </div>
          {/if}
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .species-comparison {
    border: 1px solid var(--border-100);
    border-radius: 0.5rem;
    background: var(--color-base-100);
    overflow: hidden;
  }

  .comparison-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border-100);
    background: var(--color-base-200);
  }

  .comparison-close {
    padding: 0.25rem;
    border-radius: 0.25rem;
    opacity: 0.5;
    cursor: pointer;
    background: none;
    border: none;
    color: inherit;
  }

  .comparison-close:hover {
    opacity: 1;
    background: var(--color-base-300);
  }

  /* Single row of species cards */
  .species-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.75rem;
    overflow-x: auto;
    border-bottom: 1px solid var(--border-100);
  }

  .species-card {
    display: flex;
    flex-direction: column;
    padding: 0.5rem 0.75rem;
    border-radius: 0.375rem;
    border: 1px solid var(--border-100);
    background: var(--color-base-100);
    cursor: pointer;
    text-align: left;
    color: inherit;
    transition: all 0.15s;
    flex-shrink: 0;
    min-width: 100px;
  }

  .species-card:hover {
    background: var(--color-base-200);
  }

  .species-card.selected {
    border-color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 10%, var(--color-base-100));
  }

  .species-card.focal {
    background: var(--color-base-200);
  }

  .species-common {
    font-weight: 500;
    font-size: 0.75rem;
    white-space: nowrap;
  }

  .species-scientific {
    font-size: 0.65rem;
    opacity: 50;
    font-style: italic;
  }

  .species-relationship {
    font-size: 0.6rem;
    opacity: 40;
    text-transform: capitalize;
  }

  .species-summary {
    font-size: 0.6rem;
    opacity: 50;
    line-height: 1.3;
    margin-top: 0.125rem;
    white-space: normal;
  }

  .vs-divider {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0.25rem 0.5rem;
    font-size: 0.65rem;
    opacity: 40;
    flex-shrink: 0;
  }

  /* Comparison panel */
  .comparison-panel {
    padding: 0.75rem;
  }

  .panel-title {
    font-size: 0.75rem;
    font-weight: 600;
    margin-bottom: 0.75rem;
    text-align: center;
    opacity: 70;
  }

  .section {
    border: 1px solid var(--border-100);
    border-radius: 0.375rem;
    margin-bottom: 0.5rem;
    overflow: hidden;
  }

  .section-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.5rem 0.75rem;
    background: var(--color-base-200);
    border: none;
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 600;
    text-align: left;
    color: inherit;
  }

  .section-header:hover {
    background: var(--color-base-300);
  }

  .section-content {
    padding: 0.75rem;
    background: var(--color-base-100);
  }

  .comparison-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.75rem;
  }

  .comparison-side {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .comparison-side.focal {
    padding-right: 0.5rem;
    border-right: 1px solid var(--border-100);
  }

  .side-label {
    font-size: 0.65rem;
    font-weight: 600;
    opacity: 60;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .side-content {
    font-size: 0.75rem;
    line-height: 1.4;
    opacity: 80;
  }
</style>
