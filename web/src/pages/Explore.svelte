<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import ResultCard from '../lib/ui/ResultCard.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import Skeleton from '../lib/ui/Skeleton.svelte';
  import Toast from '../lib/ui/Toast.svelte';
  import {
    addTitle,
    failureKey,
    failureKeyForCode,
    searchTitles,
    titlePath,
    type ApiKind,
    type SearchGroup,
    type SearchHit,
  } from '../lib/core/catalog';
  import { t, type MessageKey } from '../lib/core/i18n';
  import { navigate, type Match } from '../lib/core/router';
  import { session } from '../lib/core/session.svelte';
  import { createToaster } from '../lib/core/toast.svelte.ts';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const DEBOUNCE_MS = 300;
  const kinds: Array<{ kind: ApiKind; heading: MessageKey; none: MessageKey }> = [
    { kind: 'movie', heading: 'explore.movies', none: 'explore.noMovies' },
    { kind: 'show', heading: 'explore.shows', none: 'explore.noShows' },
    { kind: 'artist', heading: 'explore.artists', none: 'explore.noArtists' },
  ];

  const role = $derived(session.me?.role);
  const isOwner = $derived(role === 'owner');
  const canAdd = $derived(role === 'owner' || role === 'admin');

  const urlQuery = $derived((match.query.get('q') ?? '').trim());
  let text = $state(untrack(() => (match.query.get('q') ?? '').trim()));
  let input: HTMLInputElement | undefined = $state();
  let timer: number | undefined;

  // The address is the source of truth (the top search and the phone header link here with ?q=).
  $effect(() => {
    const q = urlQuery;
    if (q !== untrack(() => text).trim()) text = q;
  });

  onMount(() => {
    // Nothing searched yet: land in the field so typing starts at once.
    if (!urlQuery) input?.focus();
    return () => window.clearTimeout(timer);
  });

  function go(q: string, immediate: boolean): void {
    window.clearTimeout(timer);
    const apply = (): void => navigate(q ? `/explore?q=${encodeURIComponent(q)}` : '/explore', { replace: true });
    if (immediate) apply();
    else timer = window.setTimeout(apply, DEBOUNCE_MS);
  }

  function oninput(): void {
    go(text.trim(), false);
  }

  function onsubmit(event: SubmitEvent): void {
    event.preventDefault();
    go(text.trim(), true);
  }

  // One request per kind, so each row fills as its provider answers (TVmaze quickly, MusicBrainz
  // slowly) instead of every row waiting for the slowest.
  type Slot = { loading: boolean; group?: SearchGroup };
  const empty = (): Record<ApiKind, Slot> => ({
    movie: { loading: false },
    show: { loading: false },
    artist: { loading: false },
  });
  let slots = $state<Record<ApiKind, Slot>>(empty());
  let attempt = $state(0);

  $effect(() => {
    const q = urlQuery;
    void attempt;
    if (!q) {
      slots = empty();
      return;
    }
    const controller = new AbortController();
    slots = { movie: { loading: true }, show: { loading: true }, artist: { loading: true } };
    for (const { kind } of kinds) {
      void searchTitles(q, kind, controller.signal).then(
        (group) => {
          if (!controller.signal.aborted) slots[kind] = { loading: false, group };
        },
        () => {
          // Aborted by a newer search: nothing to show.
        },
      );
    }
    return () => controller.abort();
  });

  const searching = $derived(kinds.some(({ kind }) => slots[kind].loading));
  const total = $derived(
    kinds.reduce((sum, { kind }) => {
      const group = slots[kind].group;
      return sum + (group && group.results ? group.results.length : 0);
    }, 0),
  );
  const status = $derived(
    !urlQuery
      ? ''
      : searching
        ? t('explore.searching')
        : total === 1
          ? t('explore.countOne', { q: urlQuery })
          : t('explore.count', { n: total, q: urlQuery }),
  );

  // ---- Add (owner and admin) and Ask (member) ----
  const toaster = createToaster();
  let adding = $state<string | null>(null);
  let askOpen = $state(false);
  let askHit = $state<SearchHit | undefined>();

  const hitKey = (hit: SearchHit): string => `${hit.provider}:${hit.providerId}`;

  async function add(hit: SearchHit): Promise<void> {
    if (adding) return;
    adding = hitKey(hit);
    try {
      const saved = await addTitle({
        kind: hit.kind,
        provider: hit.provider,
        providerId: hit.providerId,
      });
      navigate(titlePath(saved.kind, saved.id));
    } catch (err) {
      toaster.show(t(failureKey(err)));
    } finally {
      adding = null;
    }
  }

  function ask(hit: SearchHit): void {
    askHit = hit;
    askOpen = true;
  }

  const askTarget = $derived(
    askHit?.inLibrary && askHit.titleId
      ? { titleId: askHit.titleId }
      : askHit
        ? { kind: askHit.kind, provider: askHit.provider, providerId: askHit.providerId }
        : {},
  );

  const uid = $props.id();
  const inputId = `explore-q-${uid}`;
  const statusId = `explore-status-${uid}`;
</script>

<PageHeader
  title={t('route.explore')}
  subtitle={canAdd ? t('explore.sub.add') : t('explore.sub.ask')}
/>

<form class="search" role="search" {onsubmit}>
  <label class="sr-only" for={inputId}>{t('search.label')}</label>
  <span class="glyph"><Icon name="search" size={20} /></span>
  <input
    id={inputId}
    type="search"
    bind:value={text}
    bind:this={input}
    {oninput}
    placeholder={t('search.placeholder')}
    autocomplete="off"
    enterkeyhint="search"
    aria-describedby={statusId}
  />
</form>

<p id={statusId} class="status" aria-live="polite">{status}</p>

{#if !urlQuery}
  <Panel tag="div" padding="22px">
    <p class="hint-title">{t('explore.empty.title')}</p>
    <p class="hint">{t('explore.empty.body')}</p>
  </Panel>
{:else}
  {#each kinds as { kind, heading, none } (kind)}
    {@const slot = slots[kind]}
    {@const group = slot.group}
    {@const headingId = `explore-${kind}-${uid}`}
    <section class="group" aria-labelledby={headingId}>
      <SectionHead title={t(heading)} id={headingId} />
      {#if slot.loading}
        <div class="grid" role="status" aria-busy="true">
          <span class="sr-only">{t('load.loading')}</span>
          <Skeleton height="156px" radius="var(--radius-lg)" />
          <Skeleton height="156px" radius="var(--radius-lg)" />
        </div>
      {:else if group && group.results}
        {#if group.results.length === 0}
          <p class="none">{t(none, { q: urlQuery })}</p>
        {:else}
          <ul class="grid">
            {#each group.results as hit (hitKey(hit))}
              <li>
                <ResultCard
                  {hit}
                  mode={canAdd ? 'add' : 'ask'}
                  busy={adding === hitKey(hit)}
                  onadd={add}
                  onask={ask}
                />
              </li>
            {/each}
          </ul>
        {/if}
      {:else if group && group.error === 'tmdb_key_missing'}
        <!-- No TMDB key yet: the owner can fix it, everyone else is told it is not set up. -->
        <Panel tag="div" padding="16px 18px">
          {#if isOwner}
            <p class="note">{t('explore.keyMissing.owner')}</p>
            <div class="note-action">
              <Button size="sm" href="/settings?tab=title-info">{t('explore.keyMissing.link')}</Button>
            </div>
          {:else}
            <p class="note">{t('explore.keyMissing.member')}</p>
          {/if}
        </Panel>
      {:else if group}
        <Panel tag="div" padding="16px 18px">
          <p class="note" role="alert">{t(failureKeyForCode(group.error))}</p>
          <div class="note-action">
            <Button size="sm" onclick={() => (attempt += 1)}>{t('load.retry')}</Button>
          </div>
        </Panel>
      {/if}
    </section>
  {/each}
{/if}

<AskSheet
  bind:open={askOpen}
  title={askHit?.name ?? ''}
  target={askTarget}
  kind={askHit?.kind ?? 'movie'}
/>
<Toast message={toaster.message} />

<style>
  .search {
    position: relative;
    max-width: 640px;
  }
  .glyph {
    position: absolute;
    left: 16px;
    top: 16px;
    display: flex;
    color: var(--muted);
    pointer-events: none;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    height: 52px;
    padding: 0 16px 0 46px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 16px;
    font-weight: 400;
    line-height: 1;
    color-scheme: dark;
  }
  input::placeholder {
    color: var(--muted);
    opacity: 1;
  }
  .status {
    min-height: 18px;
    margin: 10px 0 18px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .hint-title {
    font-size: 15px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
  }
  .hint {
    margin-top: 2px;
    font-size: 13.5px;
    line-height: 20px;
    color: var(--text-3);
  }
  .group {
    margin-bottom: 26px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 380px), 1fr));
    gap: 12px;
  }
  .none,
  .note {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-2);
  }
  .note-action {
    margin-top: 12px;
  }
</style>
