<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import NotSetUp from './NotSetUp.svelte';
  import Panel from './Panel.svelte';
  import Skeleton from './Skeleton.svelte';
  import { isRouteMissing } from '../core/catalog';
  import { debounce, onEvent, onResync } from '../core/events';
  import { formatDay } from '../core/format';
  import { historyIcon, historyText } from '../core/historyText';
  import { t } from '../core/i18n';
  import { getTitleHistory, type HistoryItem } from '../core/serversApi';

  interface Props {
    titleId: number;
  }

  let { titleId }: Props = $props();

  const uid = $props.id();
  const headId = `history-${uid}`;

  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let status = $state<Status>('loading');
  let items = $state.raw<HistoryItem[]>([]);

  async function load(): Promise<void> {
    try {
      items = await getTitleHistory(titleId);
      status = 'ready';
    } catch (err) {
      if (isRouteMissing(err)) status = 'off';
      else if (status !== 'ready') status = 'failed';
    }
  }

  $effect(() => {
    void titleId;
    status = 'loading';
    void load();
  });

  onMount(() => {
    // A new ask, a grab or an import adds a line: ask again a moment after the stream says so.
    const soon = debounce(() => void load(), 500);
    const stopRequests = onEvent('requests', () => soon.call());
    const stopDownloads = onEvent('downloads', (raw) => {
      const type = (raw as { type?: string }).type;
      if (type && type !== 'progress') soon.call();
    });
    // Files that arrive, are renamed or are deleted add a line too.
    const stopLibrary = onEvent('library', () => soon.call());
    const stopResync = onResync(() => soon.call());
    return () => {
      soon.cancel();
      stopRequests();
      stopDownloads();
      stopLibrary();
      stopResync();
    };
  });
</script>

<Panel labelledby={headId} padding="18px 20px 12px">
  <h2 id={headId} class="plain-head">{t('title.history')}</h2>
  {#if status === 'loading'}
    <div class="loading" role="status" aria-busy="true">
      <span class="sr-only">{t('load.loading')}</span>
      <Skeleton height="40px" radius="var(--radius-md)" />
      <Skeleton height="40px" radius="var(--radius-md)" />
    </div>
  {:else if status === 'off'}
    <NotSetUp />
  {:else if status === 'failed'}
    <p class="empty" role="alert">{t('load.failed')}</p>
  {:else if items.length === 0}
    <p class="empty">{t('history.empty')}</p>
  {:else}
    <ol class="history">
      {#each items as event, i (event.id ?? i)}
        <li>
          <span class="date">{formatDay(event.at)}</span>
          <span class="event-icon"><Icon name={historyIcon[event.event] ?? 'check'} size={16} stroke={1.9} /></span>
          <span class="event-text">{historyText(event)}</span>
        </li>
      {/each}
    </ol>
  {/if}
</Panel>

<style>
  .plain-head {
    margin-bottom: 12px;
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .loading {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-bottom: 8px;
  }
  .empty {
    padding-bottom: 8px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .history {
    border-bottom: 1px solid var(--hairline-strong);
  }
  .history li {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 10px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .date {
    flex: 0 0 64px;
    font-size: 13px;
    font-weight: 600;
    line-height: 20px;
    font-variant-numeric: tabular-nums;
    color: var(--text);
  }
  .event-icon {
    display: flex;
    margin-top: 2px;
    color: var(--muted);
  }
  .event-text {
    flex: 1 1 auto;
    min-width: 0;
    font-size: 13.5px;
    line-height: 20px;
    color: var(--text-2);
    overflow-wrap: anywhere;
  }
</style>
