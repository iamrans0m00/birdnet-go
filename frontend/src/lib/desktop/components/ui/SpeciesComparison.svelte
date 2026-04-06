<script lang="ts">
  import { X } from '@lucide/svelte';
  import { t, getLocale } from '$lib/i18n';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import type { SimilarSpeciesEntry, SpeciesGuideData } from '$lib/types/species';
  import { parseGuideDescription } from '$lib/types/species';
  import CollapsibleSection from './CollapsibleSection.svelte';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.ui;

  interface Props {
    scientificName: string;
    commonName: string;
    onclose: () => void;
  }

  let { scientificName, commonName, onclose }: Props = $props();

  let similarSpecies = $state<SimilarSpeciesEntry[]>([]);
  let isLoading = $state(true);
  let selectedSpecies = $state<string | null>(null);
  let selectedGuide = $state<SpeciesGuideData | null>(null);
  let isLoadingGuide = $state(false);

  async function fetchSimilarSpecies() {
    isLoading = true;
    try {
      const locale = getLocale();
      const encodedName = encodeURIComponent(scientificName);
      const url = buildAppUrl(`/api/v2/species/${encodedName}/similar?locale=${locale}`);
      const response = await fetch(url);
      if (response.ok) {
        const data = await response.json();
        similarSpecies = data.similar ?? [];
      }
    } catch (err) {
      logger.error('Failed to fetch similar species', err);
    } finally {
      isLoading = false;
    }
  }

  async function selectSpecies(entry: SimilarSpeciesEntry) {
    if (selectedSpecies === entry.scientific_name) {
      selectedSpecies = null;
      selectedGuide = null;
      return;
    }
    selectedSpecies = entry.scientific_name;
    selectedGuide = null;
    isLoadingGuide = true;
    try {
      const locale = getLocale();
      const encodedName = encodeURIComponent(entry.scientific_name);
      const url = buildAppUrl(`/api/v2/species/${encodedName}/guide?locale=${locale}`);
      const response = await fetch(url);
      if (response.ok) {
        selectedGuide = await response.json();
      }
    } catch (err) {
      logger.error('Failed to fetch guide for similar species', err);
    } finally {
      isLoadingGuide = false;
    }
  }

  $effect(() => {
    fetchSimilarSpecies();
  });
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
    <p class="p-4 text-sm opacity-50">{t('analytics.species.similar.empty')}</p>
  {:else}
    <div class="comparison-grid">
      <!-- Source species column -->
      <div class="comparison-column comparison-source">
        <div class="column-header">
          <span class="font-semibold text-xs uppercase tracking-wide">{commonName}</span>
          <span class="text-xs opacity-50 italic">{scientificName}</span>
        </div>
      </div>

      <!-- Similar species list -->
      <div class="comparison-column comparison-similar">
        <div class="similar-list">
          {#each similarSpecies as entry (entry.scientific_name)}
            <button
              class="similar-item"
              class:active={selectedSpecies === entry.scientific_name}
              onclick={() => selectSpecies(entry)}
            >
              <span class="font-medium text-sm">{entry.common_name}</span>
              <span class="text-xs opacity-50 italic">{entry.scientific_name}</span>
              {#if entry.guide_summary}
                <p class="text-xs opacity-60 mt-1 line-clamp-2">{entry.guide_summary}</p>
              {/if}
            </button>
          {/each}
        </div>
      </div>
    </div>

    <!-- Expanded guide for selected species -->
    {#if selectedSpecies && isLoadingGuide}
      <div class="flex items-center gap-2 p-4 text-sm opacity-60">
        <div
          class="animate-spin h-4 w-4 border-2 border-[var(--color-primary)] border-t-transparent rounded-full"
        ></div>
        <span>{t('analytics.species.guide.loading')}</span>
      </div>
    {:else if selectedGuide?.description}
      <div class="selected-guide">
        <h4 class="text-sm font-semibold mb-2">{selectedGuide.common_name}</h4>
        <div class="space-y-2">
          {#each parseGuideDescription(selectedGuide.description) as section, i (i)}
            {#if section.heading}
              <CollapsibleSection
                title={section.heading}
                defaultOpen={false}
                className="bg-[var(--color-base-200)] shadow-none rounded-lg"
                titleClassName="text-xs font-semibold uppercase tracking-wide min-h-8 py-1 px-3"
                contentClassName="px-3 pb-2"
              >
                {#if section.body}
                  <p class="text-sm leading-relaxed opacity-85">{section.body}</p>
                {/if}
              </CollapsibleSection>
            {:else if section.body}
              <p class="text-sm leading-relaxed opacity-85">{section.body}</p>
            {/if}
          {/each}
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

  .comparison-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1px;
    background: var(--border-100);
  }

  .comparison-column {
    background: var(--color-base-100);
    padding: 0.75rem;
  }

  .column-header {
    display: flex;
    flex-direction: column;
    gap: 0.125rem;
  }

  .similar-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .similar-item {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    padding: 0.5rem 0.75rem;
    border-radius: 0.375rem;
    border: 1px solid var(--border-100);
    background: var(--color-base-100);
    cursor: pointer;
    text-align: left;
    width: 100%;
    color: inherit;
    transition:
      background-color 0.15s,
      border-color 0.15s;
  }

  .similar-item:hover {
    background: var(--color-base-200);
  }

  .similar-item.active {
    border-color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 8%, var(--color-base-100));
  }

  .selected-guide {
    padding: 1rem;
    border-top: 1px solid var(--border-100);
    background: var(--color-base-100);
  }
</style>
