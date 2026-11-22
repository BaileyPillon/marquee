<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import Thumb from '../../lib/ui/Thumb.svelte';
  import { titlePath } from '../../lib/core/catalog';
  import { t } from '../../lib/core/i18n';
  import type { RequestItem } from '../../lib/core/requestsApi';
  import { decidedLine, decidedWhen } from '../../lib/core/requestView';

  interface Props {
    items: RequestItem[];
    /** Offer "Cancel" on a request that still waits (a member's own asks). */
    oncancel?: (item: RequestItem) => void;
    /** The id of a request whose cancel is under way. */
    busyId?: number | null;
  }

  let { items, oncancel, busyId = null }: Props = $props();
</script>

<Panel tag="div" padding="0" clip>
  <ul>
    {#each items as row (row.id)}
      <li>
        <Thumb
          title={row.title.name}
          width={32}
          height={42}
          fontSize={20}
          imageRef={row.title.posterRef || undefined}
        />
        <div class="text">
          <div class="title">
            <a href={titlePath(row.title.kind, row.title.id)}>{row.title.name}</a>
          </div>
          <div class="detail">{decidedLine(row)}</div>
        </div>
        {#if row.state === 'declined' || row.state === 'failed'}
          <Chip tone="inverse" size="status">
            {#snippet icon()}<Icon name="close" size={12} stroke={2.6} />{/snippet}
            {row.state === 'failed' ? t('requests.failed') : t('requests.declined')}
          </Chip>
        {:else if row.state === 'pending'}
          <Chip tone="dashed" size="status">
            {#snippet icon()}<Icon name="time" size={12} stroke={2.4} />{/snippet}
            {t('requests.waitingChip')}
          </Chip>
        {:else if row.state === 'cancelled'}
          <Chip tone="faint" size="status">{t('requests.cancelled')}</Chip>
        {:else}
          <Chip tone="solid" size="status">
            {#snippet icon()}<Icon name="check" size={12} stroke={2.6} />{/snippet}
            {row.state === 'available'
              ? t('requests.available')
              : row.state === 'partial'
                ? t('requests.partial')
                : t('requests.approved')}
          </Chip>
        {/if}
        {#if oncancel && row.state === 'pending'}
          <Button
            size="compact"
            disabled={busyId === row.id}
            aria-label={t('requests.cancelAria', { title: row.title.name })}
            onclick={() => oncancel(row)}
          >
            {t('requests.cancel')}
          </Button>
        {:else}
          <span class="when">{decidedWhen(row)}</span>
        {/if}
      </li>
    {/each}
  </ul>
</Panel>

<style>
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 14px;
    padding: 12px 16px;
  }
  li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  .text {
    flex: 1 1 220px;
    min-width: 0;
  }
  .title {
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .title a {
    color: inherit;
    text-decoration: none;
  }
  .title a:hover {
    text-decoration: underline;
  }
  .detail {
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .when {
    flex: 0 0 64px;
    text-align: end;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
</style>
