<script lang="ts">
  import ProgressBar from './ProgressBar.svelte';
  import StatusDot from './StatusDot.svelte';
  import { dotOf, stateText, statsOf } from '../core/downloadWords';
  import { qualityLabel } from '../core/format';
  import { t } from '../core/i18n';
  import { watchTitleDownloads } from '../core/titleDownloads.svelte';

  interface Props {
    titleId: number;
  }

  let { titleId }: Props = $props();

  // What is coming in for this title right now, moving as the downloads stream says so.
  const live = watchTitleDownloads(
    () => titleId,
    () => true,
  );
</script>

{#if live.items.length > 0}
  <ul class="now" aria-label={t('releases.coming')}>
    {#each live.items as d (d.id)}
      <li>
        <div class="top">
          <span class="name">{d.releaseTitle}</span>
          <span class="q">{qualityLabel(d.quality)}</span>
        </div>
        <ProgressBar
          value={d.progress * 100}
          label={t('downloads.progress', { title: d.releaseTitle })}
          tone={d.state === 'completed' || d.state === 'importing' ? 'accent' : 'text'}
          rounded
        />
        <div class="bottom">
          <StatusDot kind={dotOf(d.state)}>{stateText(d.state)}</StatusDot>
          <span class="stats">{statsOf(d)}</span>
        </div>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .now {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-bottom: 14px;
  }
  li {
    padding: 12px 14px;
    border-radius: var(--radius-md);
    background: var(--rail);
    box-shadow: inset 0 0 0 1px var(--hairline);
  }
  .top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 8px;
  }
  .name {
    min-width: 0;
    font-size: 13.5px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .q {
    flex: 0 0 auto;
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.06em;
    color: var(--text-3);
  }
  .bottom {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: 4px 14px;
    margin-top: 9px;
    color: var(--text-2);
  }
  .stats {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    font-variant-numeric: tabular-nums;
    color: var(--text-3);
  }
</style>
