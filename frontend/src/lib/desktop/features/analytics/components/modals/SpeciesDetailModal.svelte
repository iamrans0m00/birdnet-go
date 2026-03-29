<script lang="ts">
  import { untrack } from 'svelte';
  import { ExternalLink } from '@lucide/svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { parseLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  const logger = loggers.ui;

  interface SpeciesData {
    common_name: string;
    scientific_name: string;
    count: number;
    avg_confidence: number;
    max_confidence: number;
    first_heard: string;
    last_heard: string;
    thumbnail_url?: string;
  }

  interface SpeciesGuideData {
    scientific_name: string;
    common_name: string;
    description: string;
    conservation_status: string;
    source: {
      provider: string;
      url: string;
      license: string;
      license_url: string;
    };
    partial: boolean;
    cached_at: string;
  }

  interface Props {
    species: SpeciesData | null;
    isOpen: boolean;
    onClose?: () => void;
  }

  let { species, isOpen, onClose }: Props = $props();

  // Cache species data so content persists during the Modal close animation.
  // Updated when a new species is provided, retained when species becomes null
  // while isOpen transitions to false.
  let cachedSpecies = $state<SpeciesData | null>(null);

  // Species guide state
  let guideData = $state<SpeciesGuideData | null>(null);
  let guideLoading = $state(false);

  // Clear stale cache when the modal opens so previous species data doesn't flash.
  // The cache is only useful during the close transition (species becomes null while
  // isOpen transitions to false), not during open.
  let prevIsOpen = $state(false);
  $effect(() => {
    if (isOpen && !untrack(() => prevIsOpen)) {
      cachedSpecies = null;
      guideData = null;
    }
    prevIsOpen = isOpen;
  });

  $effect(() => {
    if (species) {
      cachedSpecies = species;
    }
  });

  // Fetch guide data when species changes
  $effect(() => {
    if (species?.scientific_name) {
      fetchGuideData(species.scientific_name);
    }
  });

  // Use cached data for rendering, fall back to current prop
  let displaySpecies = $derived(species ?? cachedSpecies);

  async function fetchGuideData(scientificName: string) {
    guideLoading = true;
    guideData = null;

    try {
      const encodedName = encodeURIComponent(scientificName);
      const response = await fetch(buildAppUrl(`/api/v2/species/${encodedName}/guide`));
      if (!response.ok) {
        if (response.status !== 404) {
          logger.debug('Guide fetch failed', { status: response.status, species: scientificName });
        }
        return;
      }
      guideData = await response.json();
    } catch (err) {
      logger.debug('Guide fetch error', { species: scientificName, error: err });
    } finally {
      guideLoading = false;
    }
  }

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  function formatDate(dateString: string): string {
    if (!dateString) return '';
    const date = parseLocalDateString(dateString);
    if (!date) return '';
    return date.toLocaleDateString();
  }

  /**
   * Parse a guide description that contains `## Section` markdown headers
   * into an array of { heading, body } segments for structured rendering.
   */
  function parseGuideDescription(description: string): Array<{ heading: string; body: string }> {
    const sections: Array<{ heading: string; body: string }> = [];
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

  function handleClose() {
    if (onClose) onClose();
  }
</script>

<Modal
  isOpen={isOpen && displaySpecies !== null}
  title={displaySpecies?.common_name ?? ''}
  size="md"
  type="default"
  onClose={handleClose}
  className="sm:modal-middle"
>
  {#snippet header()}
    {#if displaySpecies}
      <div class="flex items-center justify-between">
        <div class="min-w-0">
          <h3 id="modal-title" class="font-bold text-lg truncate">
            {displaySpecies.common_name}
          </h3>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 italic truncate">
            {displaySpecies.scientific_name}
          </p>
        </div>
      </div>
    {/if}
  {/snippet}

  {#snippet children()}
    {#if displaySpecies}
      {#if displaySpecies.thumbnail_url}
        <div class="w-full aspect-[4/3] rounded-xl overflow-hidden bg-[var(--color-base-300)]">
          <img
            src={displaySpecies.thumbnail_url}
            alt={displaySpecies.common_name}
            class="w-full h-full object-cover"
          />
        </div>
      {/if}

      {#if guideLoading}
        <div class="mt-3 flex items-center gap-2 text-sm opacity-60">
          <div
            class="animate-spin h-4 w-4 border-2 border-[var(--color-primary)] border-t-transparent rounded-full"
          ></div>
          <span>{t('analytics.species.guide.loading')}</span>
        </div>
      {:else if guideData?.description}
        <div class="mt-3 space-y-1">
          {#each parseGuideDescription(guideData.description) as section, i (i)}
            {#if section.heading}
              <h4
                class="text-xs font-semibold uppercase tracking-wide text-[var(--color-base-content)] opacity-55 mt-3 mb-1"
              >
                {section.heading}
              </h4>
            {/if}
            {#if section.body}
              <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-85">
                {section.body}
              </p>
            {/if}
          {/each}
          {#if guideData.conservation_status}
            <div class="flex items-center gap-2 text-xs mt-2">
              <span class="px-2 py-0.5 rounded-full bg-[var(--color-base-200)] font-medium">
                {guideData.conservation_status}
              </span>
            </div>
          {/if}
          {#if guideData.source.url}
            <div class="flex items-center gap-1 text-xs opacity-50 mt-2">
              <span>{t('analytics.species.guide.source')}</span>
              <a
                href={guideData.source.url}
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center gap-0.5 underline hover:opacity-80"
              >
                {guideData.source.provider}
                <ExternalLink class="h-3 w-3" />
              </a>
              {#if guideData.source.license}
                <span>· {guideData.source.license}</span>
              {/if}
            </div>
          {/if}
        </div>
      {/if}

      <div class="grid grid-cols-2 gap-3 text-sm mt-3">
        <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
          <span class="opacity-70">{t('analytics.species.card.detections')}</span>
          <span class="font-semibold">{displaySpecies.count}</span>
        </div>
        <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
          <span class="opacity-70">{t('analytics.species.card.confidence')}</span>
          <span class="font-semibold">{formatPercentage(displaySpecies.avg_confidence)}</span>
        </div>
        {#if displaySpecies.first_heard}
          <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.headers.firstDetected')}</span>
            <span class="font-semibold">{formatDate(displaySpecies.first_heard)}</span>
          </div>
        {/if}
        {#if displaySpecies.last_heard}
          <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.headers.lastDetected')}</span>
            <span class="font-semibold">{formatDate(displaySpecies.last_heard)}</span>
          </div>
        {/if}
      </div>
    {/if}
  {/snippet}

  {#snippet footer()}
    <button
      class="px-4 py-2 rounded-lg font-medium transition-colors w-full
             bg-[var(--color-primary)] text-[var(--color-primary-content)]
             hover:bg-[var(--color-primary)]/90"
      onclick={handleClose}
    >
      {t('common.close')}
    </button>
  {/snippet}
</Modal>
