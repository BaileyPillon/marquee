<script module lang="ts">
  export interface LabelValueItem {
    label: string;
    value: string;
    /** plain: text. on / off: a pill, solid or dashed (the text still says On or Off). */
    tone?: 'plain' | 'on' | 'off';
  }
</script>

<script lang="ts">
  import Chip from './Chip.svelte';

  interface Props {
    items: LabelValueItem[];
    /**
     * ruled: a hairline between rows (facts, rules). plain: no lines, tight rows (people and limits).
     */
    variant?: 'ruled' | 'plain';
    /** Row padding when ruled: compact 9px, cozy 10px, roomy 11px. */
    density?: 'compact' | 'cozy' | 'roomy';
    /** Space above the list, in px. Defaults to 12 when ruled and 4 when plain. */
    gap?: number;
  }

  let { items, variant = 'ruled', density = 'compact', gap }: Props = $props();

  const top = $derived(gap ?? (variant === 'ruled' ? 12 : 4));
</script>

<dl class="list {variant} {density}" style:margin-top="{top}px">
  {#each items as row (row.label)}
    <div class="row">
      <dt>{row.label}</dt>
      <dd>
        {#if row.tone === 'on'}
          <Chip tone="solid" size="value">{row.value}</Chip>
        {:else if row.tone === 'off'}
          <Chip tone="dashed" size="value">{row.value}</Chip>
        {:else}
          {row.value}
        {/if}
      </dd>
    </div>
  {/each}
</dl>

<style>
  .list {
    margin: 0;
    padding: 0;
  }
  .row {
    display: flex;
    justify-content: space-between;
    gap: 16px;
  }
  dt {
    margin: 0;
    flex: 0 0 auto;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  dd {
    margin: 0;
    text-align: right;
    font-size: 14px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text);
  }

  .ruled {
    border-bottom: 1px solid var(--hairline-strong);
  }
  .ruled .row {
    align-items: baseline;
    border-top: 1px solid var(--hairline-strong);
  }
  .ruled.compact .row {
    padding: 9px 0;
  }
  .ruled.cozy .row {
    padding: 10px 0;
  }
  .ruled.roomy .row {
    padding: 11px 0;
  }
  .ruled.roomy dd {
    line-height: 19px;
  }

  .plain .row {
    align-items: center;
    gap: 12px;
    min-height: 26px;
  }
  .plain dd {
    font-size: 13px;
  }
</style>
