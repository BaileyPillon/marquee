<script lang="ts">
  import type { Snippet } from 'svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import StatusDot from '../../lib/ui/StatusDot.svelte';
  import { t } from '../../lib/core/i18n';
  import type { ConnectionRow } from '../../lib/core/views';

  interface Props {
    title: string;
    rows: ConnectionRow[];
    /** A button at the right of the heading (Add source). */
    action?: Snippet;
    /** A line under the rows. */
    note?: string;
  }

  let { title, rows, action, note }: Props = $props();

  const uid = $props.id();
  const headId = `conn-${uid}`;
</script>

<Panel labelledby={headId} padding="18px">
  <SectionHead {title} id={headId} size="md" align="center" gap={4} minHeight={36}>
    {#if action}{@render action()}{/if}
  </SectionHead>
  <ul>
    {#each rows as row (row.name)}
      <li>
        <div class="text">
          <div class="name-row">
            <span class="name">{row.name}</span>
            {#if row.tag}<Chip tone="faint" size="micro">{row.tag}</Chip>{/if}
          </div>
          <div class="status">
            <StatusDot kind={row.state === 'on' ? 'solid' : 'hollow'}>{row.status}</StatusDot>
          </div>
        </div>
        <Button
          size="compact"
          aria-label={t('settings.rowAria', {
            action: t(`settings.action.${row.action}`),
            name: row.name,
          })}
        >
          {t(`settings.action.${row.action}`)}
        </Button>
      </li>
    {/each}
  </ul>
  {#if note}<p class="note">{note}</p>{/if}
</Panel>

<style>
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 10px 14px;
    padding: 14px 0;
  }
  li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  li :global(.btn) {
    padding: 0 16px;
  }
  .text {
    flex: 1 1 220px;
    min-width: 0;
  }
  .name-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
  }
  .name {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .status {
    margin-top: 3px;
    color: var(--text-3);
  }
  .note {
    margin-top: 12px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
</style>
