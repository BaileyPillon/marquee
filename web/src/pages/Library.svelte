<script lang="ts">
  import { onMount } from 'svelte';
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import PosterCard from '../lib/ui/PosterCard.svelte';
  import Skeleton from '../lib/ui/Skeleton.svelte';
  import { listTitles, titlePath, type ApiKind, type SortOrder, type Title } from '../lib/core/catalog';
  import { formatAdded, posterYear } from '../lib/core/format';
  import { t } from '../lib/core/i18n';
  import { libraryIndex } from '../lib/core/libraryIndex.svelte.ts';
  import { statusOfTitle } from '../lib/core/posterItems';
  import type { Match } from '../lib/core/router';
  import { session } from '../lib/core/session.svelte';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const PAGE = 60;
  const DEBOUNCE_MS = 300;

  const kind = $derived<ApiKind>(
    match.route.name === 'tv' ? 'show' : match.route.name === 'music' ? 'artist' : 'movie',
  );
  const canAdd = $derived(session.me?.role === 'owner' || session.me?.role === 'admin');

  let sort = $state<SortOrder>('added');
  let filter = $state('');
  let query = $state('');
  let timer: number | undefined;

  function onfilter(): void {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => (query = filter.trim()), DEBOUNCE_MS);
  }

  let items = $state.raw<Title[]>([]);
  let total = $state(0);
  let loading = $state(true);
  let failed = $state(false);
  let more = $state(false);
  let attempt = $state(0);

  onMount(() => {
    libraryIndex.ensure();
    return () => window.clearTimeout(timer);
  });

  $effect(() => {
    const k = kind;
    const order = sort;
    const q = query;
    void attempt;
    const controller = new AbortController();
    loading = true;
    failed = false;
    listTitles({ kind: k, sort: order, ...(q ? { q } : {}), limit: PAGE }, controller.signal).then(
      (res) => {
        items = res.items;
        total = res.total;
        loading = false;
      },
      () => {
        if (controller.signal.aborted) return;
        failed = true;
        loading = false;
      },
    );
    return () => controller.abort();
  });

  async function showMore(): Promise<void> {
    if (more) return;
    more = true;
    try {
      const res = await listTitles({
        kind,
        sort,
        ...(query ? { q: query } : {}),
        limit: PAGE,
        offset: items.length,
      });
      items = [...items, ...res.items];
      total = res.total;
    } catch {
      failed = true;
    } finally {
      more = false;
    }
  }

  const heading = $derived(
    kind === 'show' ? t('route.tv') : kind === 'artist' ? t('route.music') : t('route.movies'),
  );
  const empty = $derived(!loading && !failed && total === 0 && !query);
  const uid = $props.id();
  const filterId = `lib-filter-${uid}`;
  const sortId = `lib-sort-${uid}`;
</script>

<PageHeader
  title={heading}
  subtitle={loading && items.length === 0 ? '' : total === 1 ? t('library.countOne') : t('library.count', { n: total })}
/>

{#if !empty}
  <div class="controls">
    <div class="filter">
      <label class="sr-only" for={filterId}>{t('library.filterLabel', { what: heading })}</label>
      <span class="glyph"><Icon name="search" size={18} /></span>
      <input
        id={filterId}
        type="search"
        bind:value={filter}
        oninput={onfilter}
        placeholder={t('library.filterPlaceholder')}
        autocomplete="off"
      />
    </div>
    <div class="sort">
      <label for={sortId}>{t('library.sort')}</label>
      <select id={sortId} bind:value={sort}>
        <option value="added">{t('library.sort.added')}</option>
        <option value="name">{t('library.sort.name')}</option>
        <option value="year">{t('library.sort.year')}</option>
      </select>
    </div>
  </div>
{/if}

{#if failed && items.length === 0}
  <LoadFailed onretry={() => (attempt += 1)} />
{:else if loading && items.length === 0}
  <div class="grid" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    {#each Array.from({ length: 10 }, (_, i) => i) as i (i)}
      <Skeleton height="224px" width="150px" radius="var(--radius-sm)" />
    {/each}
  </div>
{:else if empty}
  <Panel tag="div" padding="22px">
    <p class="empty-title">
      {kind === 'show' ? t('library.empty.show') : kind === 'artist' ? t('library.empty.artist') : t('library.empty.movie')}
    </p>
    <p class="empty-body">{canAdd ? t('library.empty.add') : t('library.empty.ask')}</p>
    <div class="empty-action">
      <Button variant="primary" href="/explore">
        <Icon name="explore" size={18} />
        {t('library.empty.link')}
      </Button>
    </div>
  </Panel>
{:else if items.length === 0}
  <Panel tag="div" padding="22px">
    <p class="empty-title">{t('library.noMatch', { q: query })}</p>
    <p class="empty-body">
      {t('library.noMatchBody')}
      <a href="/explore?q={encodeURIComponent(query)}">{t('library.searchExplore', { q: query })}</a>
    </p>
  </Panel>
{:else}
  <ul class="grid" aria-busy={loading}>
    {#each items as title (title.id)}
      <li>
        <PosterCard
          href={titlePath(title.kind, title.id)}
          title={title.name}
          year={posterYear(title)}
          status={statusOfTitle(title)}
          caption={formatAdded(title.addedAt)}
          imageRef={title.posterRef || undefined}
        />
      </li>
    {/each}
  </ul>
  {#if items.length < total}
    <div class="more">
      <Button disabled={more} onclick={showMore}>
        {more ? t('library.loadingMore') : t('library.more', { shown: items.length, total })}
      </Button>
    </div>
  {/if}
  {#if failed}<p class="failed" role="alert">{t('load.failed')}</p>{/if}
{/if}

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 12px 16px;
    margin-bottom: 20px;
  }
  .filter {
    position: relative;
    flex: 1 1 280px;
    max-width: 420px;
  }
  .glyph {
    position: absolute;
    left: 14px;
    top: 13px;
    display: flex;
    color: var(--muted);
    pointer-events: none;
  }
  input,
  select {
    box-sizing: border-box;
    height: 44px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 14px;
    line-height: 1;
    color-scheme: dark;
  }
  input {
    width: 100%;
    padding: 0 14px 0 42px;
  }
  input::placeholder {
    color: var(--muted);
    opacity: 1;
  }
  .sort {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .sort label {
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  select {
    min-width: 168px;
    padding: 0 12px;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, 150px);
    gap: 22px 16px;
  }
  .more {
    margin-top: 22px;
  }
  .failed {
    margin-top: 12px;
    font-size: 14px;
    color: var(--danger);
  }
  .empty-title {
    font-size: 16px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
  }
  .empty-body {
    margin-top: 4px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .empty-action {
    margin-top: 16px;
  }

  @media (max-width: 899.98px) {
    /* Two cards a row at phone width, spread across it. */
    .grid {
      justify-content: space-between;
    }
    .sort {
      flex: 1 1 100%;
    }
    select {
      flex: 1 1 auto;
    }
  }
</style>
