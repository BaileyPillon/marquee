<script lang="ts">
  import { onMount } from 'svelte';
  import Button from './Button.svelte';
  import DataTable, { type DataColumn } from './DataTable.svelte';
  import Icon from './Icon.svelte';
  import Panel from './Panel.svelte';
  import SectionHead from './SectionHead.svelte';
  import Tabs, { type TabItem } from './Tabs.svelte';
  import Toast from './Toast.svelte';
  import TitleDownloads from './TitleDownloads.svelte';
  import { ApiError } from '../core/api';
  import { failureKey, type ApiKind } from '../core/catalog';
  import { formatBytes, formatWhen } from '../core/format';
  import { formatList, t } from '../core/i18n';
  import { isDesktop } from '../core/media';
  import { findReleases, grabRelease, type FoundRelease, type ReleaseSearch } from '../core/sourcesApi';
  import { createToaster } from '../core/toast.svelte.ts';

  interface Props {
    titleId: number;
    /** The title's name, for the words around a result. */
    name: string;
    kind: ApiKind;
    /** The copy to search for when the panel opens. */
    quality?: 'hd' | 'uhd';
    /** An episode search: the season and episode to look for. */
    season?: number;
    episode?: number;
    /** No panel around it, and the cards layout: for a dialog. */
    compact?: boolean;
    /** Run the search as soon as the panel is up (the owner just asked for this episode). */
    autorun?: boolean;
    /** Called after a grab went through. */
    ongrabbed?: () => void;
  }

  let {
    titleId,
    name,
    kind,
    quality: initialQuality = 'hd',
    season,
    episode,
    compact = false,
    autorun = false,
    ongrabbed,
  }: Props = $props();

  const uid = $props.id();
  const headId = `releases-${uid}`;
  const toaster = createToaster();

  const chooses = $derived(kind !== 'artist');
  let picked = $state<'hd' | 'uhd' | undefined>();
  const quality = $derived<'hd' | 'uhd'>(!chooses ? 'hd' : (picked ?? initialQuality));
  const qualityTabs = $derived<TabItem[]>([
    { id: 'hd', label: t('ask.quality.hd.short') },
    { id: 'uhd', label: t('ask.quality.uhd.short') },
  ]);

  type Phase = 'idle' | 'searching' | 'done' | 'failed';
  let phase = $state<Phase>('idle');
  let search = $state.raw<ReleaseSearch | undefined>();
  let errorKey = $state<ReturnType<typeof failureKey> | ''>('');
  let errorText = $state('');
  let controller: AbortController | undefined;

  /** Run the search now, for the copy shown (or `forQuality`). */
  export async function run(forQuality?: 'hd' | 'uhd'): Promise<void> {
    if (forQuality) picked = forQuality;
    controller?.abort();
    const mine = (controller = new AbortController());
    phase = 'searching';
    errorKey = '';
    errorText = '';
    actionError = '';
    try {
      const res = await findReleases(
        titleId,
        {
          quality,
          ...(season !== undefined ? { season } : {}),
          ...(episode !== undefined ? { episode } : {}),
        },
        mine.signal,
      );
      if (mine.signal.aborted) return;
      search = res;
      phase = 'done';
    } catch (err) {
      if (mine.signal.aborted) return;
      phase = 'failed';
      errorKey = failureKey(err);
      errorText =
        err instanceof ApiError && (err.code === 'no_profile' || err.code === 'invalid_request') && err.message
          ? err.message
          : '';
    }
  }

  onMount(() => {
    if (autorun) void run();
    return () => controller?.abort();
  });

  // A different copy means different results: drop the old list until the owner searches again.
  function choose(id: string): void {
    picked = id === 'uhd' ? 'uhd' : 'hd';
    if (phase === 'done' || phase === 'failed') {
      phase = 'idle';
      search = undefined;
    }
  }

  // Accepted releases first (best score first), then the rejected ones.
  const rows = $derived.by(() => {
    const list = search?.results ?? [];
    return [...list].sort((a, b) => Number(b.accepted) - Number(a.accepted) || b.score - a.score);
  });
  const acceptedCount = $derived(rows.filter((r) => r.accepted).length);
  const firstAccepted = $derived(rows.find((r) => r.accepted));

  // ---- grabbing ----
  const keyOf = (r: FoundRelease): string => `${r.indexer.id}:${r.guid}`;
  let grabbing = $state<string | null>(null);
  let grabbed = $state<Record<string, true>>({});
  let actionError = $state('');
  let actionLink = $state(false);

  async function grab(r: FoundRelease): Promise<void> {
    if (grabbing) return;
    grabbing = keyOf(r);
    actionError = '';
    actionLink = false;
    try {
      const res = await grabRelease(titleId, { indexerId: r.indexer.id, guid: r.guid, quality });
      grabbed[keyOf(r)] = true;
      toaster.show(t('releases.grabbed', { release: res.release.title || r.title }));
      ongrabbed?.();
    } catch (err) {
      actionError = t(failureKey(err));
      // No download app yet: say where to add one.
      actionLink = err instanceof ApiError && err.code === 'no_grabber';
    } finally {
      grabbing = null;
    }
  }

  // ---- words ----
  function ageText(days: number | null): string {
    if (days === null) return '';
    if (days <= 0) return t('releases.age.today');
    return t('releases.age.days', { n: days });
  }

  function extras(r: FoundRelease): string {
    return [
      r.quality,
      r.protocol === 'usenet' ? t('releases.usenet') : t('releases.torrent'),
      r.seeders !== null ? t('releases.seeders', { n: r.seeders }) : '',
    ]
      .filter(Boolean)
      .join(' · ');
  }

  const columns: DataColumn[] = [
    { id: 'release', label: t('title.col.release') },
    { id: 'size', label: t('title.col.size') },
    { id: 'source', label: t('title.col.source') },
    { id: 'age', label: t('title.col.age') },
    { id: 'score', label: t('title.col.score') },
    { id: 'action', label: t('title.col.action'), hideLabel: true },
  ];
  const template = 'minmax(260px,2fr) 76px 112px 68px minmax(300px,2fr) 96px';

  const cards = $derived(compact || !$isDesktop);
  const qualityWord = $derived(quality === 'uhd' ? t('ask.quality.uhd.short') : t('ask.quality.hd.short'));
  const searched = $derived(phase === 'done' || phase === 'failed');
</script>

{#snippet grabButton(r: FoundRelease)}
  {@const done = grabbed[keyOf(r)] === true}
  <Button
    variant={r === firstAccepted && !done ? 'primary' : 'outline'}
    size="sm"
    disabled={done || grabbing !== null}
    aria-label={t('title.grabAria', { name: r.title })}
    onclick={() => grab(r)}
  >
    {#if done}
      <Icon name="check" size={16} stroke={2.4} />
      {t('releases.sent')}
    {:else}
      <Icon name="downloads" size={16} stroke={2.2} />
      {grabbing === keyOf(r) ? t('releases.grabbing') : t('title.grab')}
    {/if}
  </Button>
{/snippet}

{#snippet body()}
  {#if chooses}
    <div class="quality">
      <Tabs
        kind="nav"
        items={qualityTabs}
        value={quality}
        onchange={choose}
        label={t('releases.copy')}
        prefix="releases-{uid}"
      />
    </div>
  {/if}

  {#if !compact}<TitleDownloads {titleId} />{/if}

  <div class="live" role="status" aria-live="polite">
    {#if phase === 'searching'}
      <p class="hint">{t('releases.searching')}</p>
    {:else if phase === 'done' && rows.length > 0}
      <p class="hint">
        {t('releases.summary', { n: rows.length, fit: acceptedCount })}
        {#if search?.searchedAt}· {t('releases.searchedAt', { when: formatWhen(search.searchedAt) })}{/if}
      </p>
    {/if}
  </div>

  {#if phase === 'idle'}
    <p class="hint">{t('releases.idle', { name, quality: qualityWord })}</p>
  {:else if phase === 'failed'}
    <p class="problem" role="alert">{errorText || t(errorKey || 'error.generic')}</p>
  {:else if phase === 'done'}
    {#if search && search.indexersFailed.length > 0}
      <p class="problem" role="status">
        {t('releases.failedSources', {
          names: formatList(search.indexersFailed.map((f) => f.name)),
        })}
      </p>
    {/if}
    {#if rows.length === 0}
      {#if (search?.indexersSearched ?? 1) === 0 && (search?.indexersFailed.length ?? 0) === 0}
        <div class="empty">
          <p class="empty-title">{t('releases.noSources.title')}</p>
          <p class="hint">{t('releases.noSources.body')}</p>
          <div class="empty-action">
            <Button size="sm" href="/settings?add=source">
              <Icon name="plus" size={16} stroke={2.2} />
              {t('releases.noSources.add')}
            </Button>
          </div>
        </div>
      {:else}
        <p class="hint">{t('releases.none', { quality: qualityWord })}</p>
      {/if}
    {:else if cards}
      <ul class="cards">
        {#each rows as r (keyOf(r))}
          <li class="card" class:rejected={!r.accepted}>
            <div class="card-name">{r.title}</div>
            <div class="card-meta">
              {formatBytes(r.size)} · {r.indexer.name}{ageText(r.age) ? ` · ${ageText(r.age)}` : ''}
            </div>
            <div class="card-meta">{extras(r)}</div>
            <div class="card-why">
              <span class="score" class:best={r === firstAccepted}>{r.score}</span>
              <span class="reason">
                {#if r.accepted}
                  {r.reasons.join(' · ')}
                {:else}
                  <span class="sr-only">{t('releases.rejectedSr')}</span>{r.rejection}
                {/if}
              </span>
            </div>
            {#if r.accepted}<div class="card-grab">{@render grabButton(r)}</div>{/if}
          </li>
        {/each}
      </ul>
    {:else}
      <DataTable
        label={t('title.releases.label', { quality: qualityWord })}
        {columns}
        {template}
        minWidth={980}
        {rows}
        rowKey={keyOf}
        padBottom={6}
      >
        {#snippet cell(row: FoundRelease, column: DataColumn)}
          <span class="cell" class:rejected={!row.accepted}>
            {#if column.id === 'release'}
              <span class="release">{row.title}</span>
              <span class="extras">{extras(row)}</span>
            {:else if column.id === 'size'}
              <span class="num strong">{formatBytes(row.size)}</span>
            {:else if column.id === 'source'}
              <span class="soft">{row.indexer.name}</span>
            {:else if column.id === 'age'}
              <span class="soft num">{ageText(row.age)}</span>
            {:else if column.id === 'score'}
              <span class="score-cell">
                <span class="score" class:best={row === firstAccepted}>{row.score}</span>
                <span class="reason">
                  {#if row.accepted}
                    {row.reasons.join(' · ')}
                  {:else}
                    <span class="sr-only">{t('releases.rejectedSr')}</span>{row.rejection}
                  {/if}
                </span>
              </span>
            {:else if row.accepted}
              <span class="grab">{@render grabButton(row)}</span>
            {/if}
          </span>
        {/snippet}
      </DataTable>
    {/if}
  {/if}

  {#if actionError}
    <p class="problem" role="alert">
      {actionError}
      {#if actionLink}<a href="/settings?add=downloadApp">{t('releases.addApp')}</a>{/if}
    </p>
  {/if}
{/snippet}

{#if compact}
  {@render body()}
{:else}
  <Panel labelledby={headId} padding="18px 20px 12px">
    <SectionHead title={t('title.releases.heading')} id={headId} gap={12} align="center">
      <Button size="sm" disabled={phase === 'searching'} onclick={() => run()}>
        <Icon name="search" size={16} stroke={2.2} />
        {phase === 'searching'
          ? t('releases.searchingShort')
          : searched
            ? t('releases.searchAgain')
            : t('releases.search')}
      </Button>
    </SectionHead>
    {@render body()}
  </Panel>
{/if}

<Toast message={toaster.message} />

<style>
  .quality {
    margin-bottom: 12px;
  }
  .live {
    min-height: 0;
  }
  .hint {
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  .live .hint {
    margin-bottom: 10px;
  }
  .problem {
    margin: 4px 0 10px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--danger);
  }
  .problem a {
    margin-inline-start: 6px;
    font-weight: 600;
  }
  .empty {
    padding: 4px 0 10px;
  }
  .empty-title {
    font-size: 15px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
  }
  .empty-action {
    margin-top: 12px;
  }

  .cell {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .cell.rejected {
    opacity: 0.62;
  }
  .release {
    font-size: 13.5px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .extras {
    font-size: 12.5px;
    line-height: 17px;
    color: var(--muted);
  }
  .num {
    font-variant-numeric: tabular-nums;
  }
  .strong {
    font-size: 13.5px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
  }
  .soft {
    font-size: 13.5px;
    font-weight: 500;
    line-height: 19px;
    color: var(--text-2);
  }
  .score-cell {
    display: flex;
    flex-direction: row;
    align-items: flex-start;
    gap: 10px;
    min-width: 0;
  }
  .score {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    min-width: 36px;
    height: 26px;
    padding: 0 8px;
    border: 1px solid var(--line);
    border-radius: 5px;
    font-size: 13px;
    font-weight: 700;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    color: var(--text);
  }
  .score.best {
    background: var(--text);
    border-color: var(--text);
    color: var(--ground);
  }
  .reason {
    min-width: 0;
    padding-top: 4px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-2);
  }
  .grab {
    display: flex;
    justify-content: flex-end;
  }

  .cards {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .card {
    padding: 12px 14px;
    border-radius: var(--radius-md);
    background: var(--rail);
    box-shadow: inset 0 0 0 1px var(--hairline);
  }
  .card.rejected .card-name,
  .card.rejected .card-meta,
  .card.rejected .card-why {
    opacity: 0.62;
  }
  .card-name {
    font-size: 13.5px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .card-meta {
    margin-top: 2px;
    font-size: 12.5px;
    line-height: 17px;
    color: var(--text-3);
    overflow-wrap: anywhere;
  }
  .card-why {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    margin-top: 10px;
  }
  .card-grab {
    margin-top: 12px;
  }
  .card-grab :global(.btn) {
    width: 100%;
  }
</style>
