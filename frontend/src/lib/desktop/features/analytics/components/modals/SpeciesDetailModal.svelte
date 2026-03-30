<script lang="ts">
  import { untrack } from 'svelte';
  import { ExternalLink, BookOpen, Trash2 } from '@lucide/svelte';
  import CollapsibleSection from '$lib/desktop/components/ui/CollapsibleSection.svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t, getLocale } from '$lib/i18n';
  import { parseLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import {
    parseGuideDescription,
    type SpeciesGuideData,
    type SpeciesNoteData,
  } from '$lib/types/species';
  import { highlightSeasonKeywords } from '$lib/utils/seasonHighlight';

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

  // Species notes state
  let speciesNotes = $state<SpeciesNoteData[]>([]);
  let isLoadingNotes = $state(false);
  let isSavingNote = $state(false);
  let newNoteText = $state('');

  // Clear stale cache when the modal opens so previous species data doesn't flash.
  // The cache is only useful during the close transition (species becomes null while
  // isOpen transitions to false), not during open.
  let prevIsOpen = $state(false);
  $effect(() => {
    if (isOpen && !untrack(() => prevIsOpen)) {
      cachedSpecies = null;
      guideData = null;
      speciesNotes = [];
      newNoteText = '';
    }
    prevIsOpen = isOpen;
  });

  $effect(() => {
    if (species) {
      cachedSpecies = species;
    }
  });

  // Fetch guide data and notes when species changes
  $effect(() => {
    if (species?.scientific_name) {
      fetchGuideData(species.scientific_name);
      fetchSpeciesNotes(species.scientific_name);
    }
  });

  // Use cached data for rendering, fall back to current prop
  let displaySpecies = $derived(species ?? cachedSpecies);

  async function fetchGuideData(scientificName: string) {
    guideLoading = true;
    guideData = null;

    try {
      const encodedName = encodeURIComponent(scientificName);
      const locale = getLocale();
      const localeParam = locale && locale !== 'en' ? `?locale=${locale}` : '';
      const response = await fetch(buildAppUrl(`/api/v2/species/${encodedName}/guide${localeParam}`));
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

  async function fetchSpeciesNotes(scientificName: string) {
    isLoadingNotes = true;
    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/species/${encodeURIComponent(scientificName)}/notes`)
      );
      if (response.ok) {
        speciesNotes = await response.json();
      }
    } catch {
      // Non-critical
    } finally {
      isLoadingNotes = false;
    }
  }

  async function saveSpeciesNote() {
    if (!displaySpecies?.scientific_name || !newNoteText.trim()) return;
    isSavingNote = true;
    try {
      const response = await fetch(
        buildAppUrl(
          `/api/v2/species/${encodeURIComponent(displaySpecies.scientific_name)}/notes`
        ),
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ entry: newNoteText.trim() }),
        }
      );
      if (response.ok) {
        newNoteText = '';
        await fetchSpeciesNotes(displaySpecies.scientific_name);
      }
    } catch (err) {
      logger.error('Error saving species note', { error: err });
    } finally {
      isSavingNote = false;
    }
  }

  async function deleteSpeciesNote(noteId: number) {
    if (!displaySpecies?.scientific_name) return;
    try {
      const response = await fetch(buildAppUrl(`/api/v2/species/notes/${noteId}`), {
        method: 'DELETE',
      });
      if (response.ok) {
        await fetchSpeciesNotes(displaySpecies.scientific_name);
      }
    } catch (err) {
      logger.error('Error deleting species note', { error: err });
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
        <div class="flex items-center gap-1.5 flex-wrap mt-2 mb-1">
          {#if guideData.quality && guideData.quality !== 'full'}
            <span class="inline-block text-[0.625rem] font-semibold uppercase tracking-wider px-2 py-0.5 rounded-full
              {guideData.quality === 'intro_only' ? 'bg-amber-500/20 text-amber-600 dark:text-amber-400' : 'bg-[var(--color-base-300)] text-[var(--color-base-content)] opacity-60'}">
              {t(`analytics.species.guide.quality${guideData.quality === 'intro_only' ? 'IntroOnly' : 'Stub'}`)}
            </span>
          {/if}
          {#if guideData.expectedness}
            <span class="inline-block text-[0.625rem] font-semibold uppercase tracking-wider px-2 py-0.5 rounded-full
              {guideData.expectedness === 'expected' ? 'bg-green-500/20 text-green-600 dark:text-green-400' :
               guideData.expectedness === 'uncommon' ? 'bg-amber-500/20 text-amber-600 dark:text-amber-400' :
               'bg-red-500/20 text-red-600 dark:text-red-400'}">
              {t(`analytics.species.guide.expectedness.${guideData.expectedness}`)}
            </span>
          {/if}
          {#if guideData.current_season}
            <span class="inline-block text-[0.625rem] font-semibold uppercase tracking-wider px-2 py-0.5 rounded-full bg-[var(--color-base-200)] text-[var(--color-base-content)] opacity-70">
              {t(`analytics.species.guide.season.${guideData.current_season}`)}
            </span>
          {/if}
        </div>
        <div class="mt-3 space-y-2">
          {#each parseGuideDescription(guideData.description) as section, i (i)}
            {#if section.heading}
              <CollapsibleSection
                title={section.heading}
                defaultOpen={false}
                className="bg-[var(--color-base-200)] shadow-none rounded-lg"
                titleClassName="text-xs font-semibold uppercase tracking-wide min-h-8 py-1 px-3"
                contentClassName="px-3 pb-2"
              >
                {#if section.body}
                  <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-85">
                    {@html highlightSeasonKeywords(section.body, guideData.current_season)}
                  </p>
                {/if}
              </CollapsibleSection>
            {:else if section.body}
              <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-85">
                {@html highlightSeasonKeywords(section.body, guideData.current_season)}
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
          {#if guideData.source?.url}
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

      <!-- Species Notes -->
      <div class="mt-3">
        <div class="flex items-center gap-1.5 mb-2">
          <BookOpen class="h-3.5 w-3.5 opacity-60" />
          <span class="text-xs font-semibold uppercase tracking-wide opacity-70">
            {t('analytics.species.notes.title')}
          </span>
        </div>

        {#if speciesNotes.length > 0}
          <div class="space-y-1.5">
            {#each speciesNotes as note (note.id)}
              <div class="group flex items-start gap-2 bg-[var(--color-base-200)] rounded-lg px-3 py-2">
                <p class="text-sm leading-relaxed flex-1">{note.entry}</p>
                <button
                  class="opacity-0 group-hover:opacity-50 hover:!opacity-100 p-1 rounded text-red-500 hover:bg-red-500 hover:text-white transition-all"
                  aria-label={t('analytics.species.notes.deleteConfirm')}
                  onclick={() => deleteSpeciesNote(note.id)}
                >
                  <Trash2 class="h-3 w-3" />
                </button>
              </div>
            {/each}
          </div>
        {:else if !isLoadingNotes}
          <p class="text-xs opacity-40 italic">{t('analytics.species.notes.empty')}</p>
        {/if}

        <div class="flex gap-2 mt-2">
          <input
            type="text"
            class="flex-1 text-sm px-3 py-1.5 rounded-lg border border-[var(--border-100)] bg-[var(--color-base-100)] text-[var(--color-base-content)] focus:outline-none focus:border-[var(--color-primary)]"
            placeholder={t('analytics.species.notes.placeholder')}
            bind:value={newNoteText}
            onkeydown={(e: KeyboardEvent) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                saveSpeciesNote();
              }
            }}
          />
          <button
            class="text-xs font-semibold px-3 py-1.5 rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] disabled:opacity-40 hover:opacity-90 transition-opacity"
            disabled={!newNoteText.trim() || isSavingNote}
            onclick={saveSpeciesNote}
          >
            {isSavingNote ? t('analytics.species.notes.saving') : t('analytics.species.notes.save')}
          </button>
        </div>
      </div>

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
