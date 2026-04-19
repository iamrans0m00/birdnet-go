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

  // Unique, instance-scoped prefix so aria-controls IDs don't collide when
  // this component is rendered twice on the same page (e.g. DetectionDetail
  // while a SpeciesDetailModal is open).
  const uid = $props.id();
  const descriptionSectionId = `species-description-${uid}`;
  const songsSectionId = `species-songs-${uid}`;
  const similarSectionId = `species-similar-${uid}`;

  let similarSpecies = $state<SimilarSpeciesEntry[]>([]);
  let isLoading = $state(true);
  let focalGuide = $state<SpeciesGuideData | null>(null);
  let focalSections = $state<ReturnType<typeof parseGuideDescription>>([]);
  let selectedSimilarIndex = $state<number>(0);
  let isLoadingSimilarGuide = $state(false);
  let similarGuideSections = $state<ReturnType<typeof parseGuideDescription>>([]);
  let selectedSimilarEntry = $derived(
    selectedSimilarIndex >= 0 && selectedSimilarIndex < similarSpecies.length
      ? // eslint-disable-next-line security/detect-object-injection -- bounds-checked by the condition above
        similarSpecies[selectedSimilarIndex]
      : null
  );

  // AbortController for the per-species guide fetch — cancelled when switching species.
  let similarGuideController: AbortController | null = null;

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
    // Cancel any in-flight guide fetch from a previous selection.
    similarGuideController?.abort();
    // Fetch guide for the selected similar species
    if (index >= 0 && index < similarSpecies.length) {
      const controller = new AbortController();
      similarGuideController = controller;
      isLoadingSimilarGuide = true;
      similarGuideSections = [];
      try {
        // eslint-disable-next-line security/detect-object-injection -- index is a numeric loop counter, not user input
        const entry = similarSpecies[index];
        const encoded = encodeURIComponent(entry.scientific_name);
        const locale = getLocale();
        const localeParam = locale && locale !== 'en' ? `?locale=${locale}` : '';
        const url = buildAppUrl(`/api/v2/species/${encoded}/guide${localeParam}`);
        const response = await fetch(url, { signal: controller.signal });
        if (controller.signal.aborted) return;
        if (response.ok) {
          const data = await response.json();
          if (selectedSimilarIndex === index && data.description) {
            similarGuideSections = parseGuideDescription(data.description);
          }
        }
      } catch (err) {
        if (err instanceof Error && err.name === 'AbortError') return;
        logger.error('Failed to fetch similar species guide', err);
      } finally {
        if (similarGuideController === controller) {
          isLoadingSimilarGuide = false;
          similarGuideController = null;
        }
      }
    }
  }

  $effect(() => {
    const controller = new AbortController();
    void Promise.all([
      fetchSimilarSpecies(controller.signal),
      fetchFocalSpeciesGuide(controller.signal),
    ]);
    return () => {
      controller.abort();
      similarGuideController?.abort();
    };
  });

  // Helper to extract specific section from focal guide by heading name.
  function getSectionContent(
    sections: ReturnType<typeof parseGuideDescription>,
    heading: string
  ): string {
    const section = sections.find(s => s.heading?.toLowerCase() === heading.toLowerCase());
    return section?.body ?? '';
  }

  // Helper to get the first section with content, regardless of locale-specific heading.
  // Falls back gracefully when the heading language doesn't match a hardcoded English name.
  function getFirstSectionBody(sections: ReturnType<typeof parseGuideDescription>): string {
    for (const s of sections) {
      if (s.body) return s.body;
    }
    return '';
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
      <div class="species-card focal">
        <span class="species-common">{commonName}</span>
        <span class="species-scientific">{scientificName}</span>
      </div>
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
        <span>{t('analytics.species.guide.vs')}</span>
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
            <span class="species-relationship"
              >{t(
                `analytics.species.similar.${entry.relationship === 'same_genus' ? 'sameGenus' : entry.relationship === 'same_family' ? 'sameFamily' : 'similar'}`
              )}</span
            >
          {/if}
          {#if entry.guide_summary}
            <span class="species-summary">{entry.guide_summary}</span>
          {/if}
        </button>
      {/each}
    </div>

    <!-- Comparison panel -->
    {#if selectedSimilarEntry !== null}
      {@const similarEntry = selectedSimilarEntry}
      {@const similarSections = similarEntry.sections ?? null}

      <div class="comparison-panel">
        <h4 class="panel-title">
          {commonName}
          {t('analytics.species.guide.vs')}
          {similarEntry.common_name}
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
            {#each similarGuideSections as section (section.heading)}
              <div class="guide-section">
                <h5 class="guide-section-heading">{section.heading}</h5>
                <p class="guide-section-body">{section.body}</p>
              </div>
            {/each}
          </div>
        {/if}

        <!-- Description Section - Expanded by default -->
        <div class="section">
          <button
            class="section-header"
            aria-expanded={descriptionOpen}
            aria-controls={descriptionSectionId}
            onclick={() => (descriptionOpen = !descriptionOpen)}
          >
            {#if descriptionOpen}
              <ChevronDown class="h-4 w-4" />
            {:else}
              <ChevronRight class="h-4 w-4" />
            {/if}
            <Bird class="h-4 w-4 text-blue-500" />
            <span>{t('analytics.species.guide.description')}</span>
          </button>
          {#if descriptionOpen}
            <div id={descriptionSectionId} class="section-content">
              <div class="comparison-row">
                <div class="comparison-side focal">
                  <span class="side-label">{commonName}</span>
                  <p class="side-content">
                    {#if focalSections.length > 0}
                      {getFirstSectionBody(focalSections) ||
                        focalGuide?.description?.substring(0, 300) ||
                        t('analytics.species.guide.noDescription')}
                    {:else}
                      {focalGuide?.description?.substring(0, 300) ||
                        t('analytics.species.guide.noDescription')}
                    {/if}
                  </p>
                </div>
                <div class="comparison-side">
                  <span class="side-label">{similarEntry.common_name}</span>
                  <p class="side-content">
                    {similarSections?.description ||
                      similarEntry.guide_summary ||
                      t('analytics.species.guide.noDescription')}
                  </p>
                </div>
              </div>
            </div>
          {/if}
        </div>

        <!-- Songs and Calls Section - Collapsed by default -->
        <div class="section">
          <button
            class="section-header"
            aria-expanded={songsOpen}
            aria-controls={songsSectionId}
            onclick={() => (songsOpen = !songsOpen)}
          >
            {#if songsOpen}
              <ChevronDown class="h-4 w-4" />
            {:else}
              <ChevronRight class="h-4 w-4" />
            {/if}
            <Music class="h-4 w-4 text-green-500" />
            <span>{t('analytics.species.guide.songs')}</span>
          </button>
          {#if songsOpen}
            <div id={songsSectionId} class="section-content">
              <div class="comparison-row">
                <div class="comparison-side focal">
                  <span class="side-label">{commonName}</span>
                  <p class="side-content">
                    {getSectionContent(focalSections, 'Songs and calls') ||
                      getSectionContent(focalSections, 'Song and calls') ||
                      getSectionContent(focalSections, 'Vocalisation') ||
                      getSectionContent(focalSections, 'Voice') ||
                      getSectionContent(focalSections, 'Stimme') ||
                      getSectionContent(focalSections, 'Chant et cris') ||
                      getSectionContent(focalSections, 'Voix') ||
                      getSectionContent(focalSections, 'Voz') ||
                      getSectionContent(focalSections, 'Canto') ||
                      getSectionContent(focalSections, 'Głos') ||
                      getSectionContent(focalSections, 'Ääntelyt') ||
                      getSectionContent(focalSections, 'Läte') ||
                      t('analytics.species.guide.noSongs')}
                  </p>
                </div>
                <div class="comparison-side">
                  <span class="side-label">{similarEntry.common_name}</span>
                  <p class="side-content">
                    {similarSections?.songs_and_calls || t('analytics.species.guide.noSongs')}
                  </p>
                </div>
              </div>
            </div>
          {/if}
        </div>

        <!-- Similar Species Section - Collapsed by default -->
        <div class="section">
          <button
            class="section-header"
            aria-expanded={similarOpen}
            aria-controls={similarSectionId}
            onclick={() => (similarOpen = !similarOpen)}
          >
            {#if similarOpen}
              <ChevronDown class="h-4 w-4" />
            {:else}
              <ChevronRight class="h-4 w-4" />
            {/if}
            <ListOrdered class="h-4 w-4 text-orange-500" />
            <span>{t('analytics.species.guide.similar')}</span>
          </button>
          {#if similarOpen}
            <div id={similarSectionId} class="section-content">
              <div class="comparison-row">
                <div class="comparison-side focal">
                  <span class="side-label">{commonName}</span>
                  <p class="side-content">
                    {#if focalSections.length > 0}
                      {getSectionContent(focalSections, 'Similar species') ||
                        getSectionContent(focalSections, 'Ähnliche Arten') ||
                        getSectionContent(focalSections, 'Espèces similaires') ||
                        getSectionContent(focalSections, 'Especies similares') ||
                        getSectionContent(focalSections, 'Verwechslungsmöglichkeiten') ||
                        t('analytics.species.guide.noSimilar')}
                    {:else}
                      {t('analytics.species.guide.noSimilar')}
                    {/if}
                  </p>
                </div>
                <div class="comparison-side">
                  <span class="side-label">{similarEntry.common_name}</span>
                  <p class="side-content">
                    {#if similarSections?.similar_species && similarSections.similar_species.length > 0}
                      {similarSections.similar_species.join(', ')}
                    {:else}
                      {t('analytics.species.guide.noSimilar')}
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
    opacity: 0.5;
    font-style: italic;
  }

  .species-relationship {
    font-size: 0.6rem;
    opacity: 0.4;
    text-transform: capitalize;
  }

  .species-summary {
    font-size: 0.6rem;
    opacity: 0.5;
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
    opacity: 0.4;
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
    opacity: 0.7;
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
    opacity: 0.6;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .side-content {
    font-size: 0.75rem;
    line-height: 1.4;
    opacity: 0.8;
  }
</style>
