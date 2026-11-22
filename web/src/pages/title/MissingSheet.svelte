<script lang="ts">
  import { untrack } from 'svelte';
  import BottomSheet from '../../lib/ui/BottomSheet.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import { ApiError } from '../../lib/core/api';
  import { failureKey, type Episode } from '../../lib/core/catalog';
  import { formatBytes } from '../../lib/core/format';
  import { t } from '../../lib/core/i18n';
  import { findReleases, grabRelease, type FoundRelease } from '../../lib/core/sourcesApi';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    titleId: number;
    name: string;
    season: number;
    /** The aired episodes of this season that have no file. */
    episodes: Episode[];
    ongrabbed?: () => void;
  }

  let { open = $bindable(false), titleId, name, season, episodes, ongrabbed }: Props = $props();

  type Step = 'waiting' | 'searching' | 'found' | 'none' | 'failed' | 'grabbing' | 'grabbed';
  interface Row {
    ep: Episode;
    step: Step;
    best?: FoundRelease;
    note?: string;
  }

  let rows = $state<Row[]>([]);
  let finished = $state(false);
  let grabbingAll = $state(false);
  let noApp = $state(false);
  let noSources = $state(false);
  let run = 0;
  let controller: AbortController | undefined;

  // Ask the sources one episode at a time (never all at once), when the dialog opens.
  $effect(() => {
    if (!open) {
      controller?.abort();
      return;
    }
    untrack(() => void search());
    return () => controller?.abort();
  });

  async function search(): Promise<void> {
    const mine = ++run;
    controller?.abort();
    const ctl = (controller = new AbortController());
    rows = episodes.map((ep) => ({ ep, step: 'waiting' as Step }));
    finished = false;
    grabbingAll = false;
    noApp = false;
    noSources = false;
    for (const row of rows) {
      if (ctl.signal.aborted || mine !== run) return;
      row.step = 'searching';
      try {
        const res = await findReleases(titleId, { quality: 'hd', season, episode: row.ep.number }, ctl.signal);
        if (ctl.signal.aborted || mine !== run) return;
        // Nothing to ask: no source is set up, so every episode would come back empty.
        if ((res.indexersSearched ?? 1) === 0 && res.indexersFailed.length === 0) {
          noSources = true;
          row.step = 'waiting';
          finished = true;
          return;
        }
        const best = res.results
          .filter((r) => r.accepted)
          .sort((a, b) => b.score - a.score)[0];
        if (best) {
          row.best = best;
          row.step = 'found';
        } else {
          row.step = 'none';
        }
      } catch (err) {
        if (ctl.signal.aborted || mine !== run) return;
        row.step = 'failed';
        row.note = t(failureKey(err));
        // The same thing will go wrong for every episode: stop asking.
        if (err instanceof ApiError && (err.code === 'no_copy' || err.code === 'no_grabber')) {
          finished = true;
          return;
        }
      }
    }
    finished = true;
  }

  const found = $derived(rows.filter((r) => r.step === 'found'));
  const done = $derived(rows.filter((r) => r.step !== 'waiting' && r.step !== 'searching').length);

  async function grabAll(): Promise<void> {
    if (grabbingAll) return;
    grabbingAll = true;
    noApp = false;
    let sent = 0;
    for (const row of rows) {
      if (row.step !== 'found' || !row.best) continue;
      row.step = 'grabbing';
      try {
        await grabRelease(titleId, { indexerId: row.best.indexer.id, guid: row.best.guid, quality: 'hd' });
        row.step = 'grabbed';
        sent += 1;
      } catch (err) {
        row.step = 'failed';
        row.note = t(failureKey(err));
        if (err instanceof ApiError && err.code === 'no_grabber') {
          noApp = true;
          break;
        }
      }
    }
    grabbingAll = false;
    if (sent > 0) ongrabbed?.();
  }

  function stepText(row: Row): string {
    switch (row.step) {
      case 'waiting':
        return t('show.missing.waiting');
      case 'searching':
        return t('show.missing.searching');
      case 'found':
        return t('show.missing.found', {
          release: row.best?.title ?? '',
          size: row.best ? formatBytes(row.best.size) : '',
          score: row.best?.score ?? 0,
        });
      case 'none':
        return t('show.missing.none');
      case 'grabbing':
        return t('releases.grabbing');
      case 'grabbed':
        return t('show.missing.grabbed');
      default:
        return row.note ?? t('error.generic');
    }
  }
</script>

<BottomSheet bind:open title={t('show.missing.title', { name, season })} wide>
  <p class="intro">{t('show.missing.intro')}</p>
  <div class="live" role="status" aria-live="polite">
    {#if episodes.length === 0}
      {t('show.missing.nothing')}
    {:else if noSources}
      {t('releases.noSources.title')}
    {:else if !finished}
      {t('show.missing.progress', { done, total: rows.length })}
    {:else}
      {t('show.missing.summary', { found: found.length, total: rows.length })}
    {/if}
  </div>
  {#if noSources}
    <p class="intro">{t('releases.noSources.body')}</p>
    <div class="actions">
      <Button size="lg" href="/settings?add=source">{t('releases.noSources.add')}</Button>
    </div>
  {:else if rows.length > 0}
    <ol class="rows">
      {#each rows as row (row.ep.id)}
        <li class={row.step}>
          <span class="ep">{t('show.episode', { n: row.ep.number })}</span>
          <span class="what">{stepText(row)}</span>
        </li>
      {/each}
    </ol>
  {/if}
  {#if noApp}
    <p class="problem" role="alert">
      {t('error.noGrabber')}
      <a href="/settings?add=downloadApp">{t('releases.addApp')}</a>
    </p>
  {/if}
  <div class="actions">
    {#if !noSources}
    <Button
      variant="primary"
      size="lg"
      block
      disabled={!finished || found.length === 0 || grabbingAll}
      onclick={grabAll}
    >
      <Icon name="downloads" size={18} stroke={2.2} />
      {grabbingAll ? t('releases.grabbing') : t('show.missing.grabAll', { n: found.length })}
    </Button>
    {/if}
    <Button variant="ghost" size="lg" block onclick={() => (open = false)}>{t('ask.close')}</Button>
  </div>
</BottomSheet>

<style>
  .intro {
    margin: 8px 0 0;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .live {
    margin: 12px 0 8px;
    min-height: 20px;
    font-size: 13.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text-2);
  }
  .rows {
    display: flex;
    flex-direction: column;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 14px;
    padding: 10px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .ep {
    flex: 0 0 96px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .what {
    flex: 1 1 220px;
    min-width: 0;
    font-size: 13.5px;
    line-height: 20px;
    color: var(--text-3);
    overflow-wrap: anywhere;
  }
  .found .what,
  .grabbed .what {
    color: var(--text-2);
  }
  .failed .what {
    color: var(--danger);
  }
  .problem {
    margin-top: 10px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--danger);
  }
  .problem a {
    margin-inline-start: 6px;
    font-weight: 600;
  }
  .actions {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 16px;
  }
</style>
