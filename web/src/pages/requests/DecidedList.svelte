<script lang="ts">
  import Chip from '../../lib/ui/Chip.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import Thumb from '../../lib/ui/Thumb.svelte';
  import { t } from '../../lib/core/i18n';
  import type { DecidedRequest } from '../../lib/core/views';

  interface Props {
    items: DecidedRequest[];
  }

  let { items }: Props = $props();
</script>

<Panel tag="div" padding="0" clip>
  <ul>
    {#each items as row (row.id)}
      <li>
        <Thumb title={row.title} width={32} height={42} fontSize={20} />
        <div class="text">
          <div class="title">{row.title}</div>
          <div class="detail">{row.detail}</div>
        </div>
        {#if row.outcome === 'approved'}
          <Chip tone="solid" size="status">
            {#snippet icon()}<Icon name="check" size={12} stroke={2.6} />{/snippet}
            {t('requests.approved')}
          </Chip>
        {:else}
          <Chip tone="inverse" size="status">
            {#snippet icon()}<Icon name="close" size={12} stroke={2.6} />{/snippet}
            {t('requests.declined')}
          </Chip>
        {/if}
        <span class="when">{row.when}</span>
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
  }
  .detail {
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .when {
    flex: 0 0 52px;
    text-align: right;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
</style>
