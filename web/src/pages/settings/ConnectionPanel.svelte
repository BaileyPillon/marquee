<script lang="ts">
  import type { Snippet } from 'svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import StatusDot from '../../lib/ui/StatusDot.svelte';
  import { t } from '../../lib/core/i18n';
  import type { PanelRow } from '../../lib/core/views';

  interface Props {
    title: string;
    rows: PanelRow[];
    /** Buttons at the right of the heading (Add source). */
    action?: Snippet;
    /** A line under the rows. */
    note?: string;
    /** What to say when there are no rows yet. */
    empty?: string;
    /** More below the rows (a form, a help line). */
    children?: Snippet;
    /** The id the heading gets, so a link such as /settings#watchlists can land on it. */
    anchor?: string;
  }

  let { title, rows, action, note, empty, children, anchor }: Props = $props();

  const uid = $props.id();
  const headId = `conn-${uid}`;
</script>

<Panel labelledby={headId} padding="18px">
  <div id={anchor}>
    <SectionHead {title} id={headId} size="md" align="center" gap={4} minHeight={36}>
      {#if action}{@render action()}{/if}
    </SectionHead>
  </div>
  {#if rows.length === 0 && empty}
    <p class="empty">{empty}</p>
  {/if}
  <ul>
    {#each rows as row (row.key)}
      <li>
        <div class="line">
          <div class="text">
            <div class="name-row">
              <span class="name">{row.name}</span>
              {#if row.tag}<Chip tone="faint" size="micro">{row.tag}</Chip>{/if}
            </div>
            <div class="status">
              <StatusDot kind={row.state === 'on' ? 'solid' : 'hollow'}>{row.status}</StatusDot>
            </div>
            {#if row.detail}<div class="detail">{row.detail}</div>{/if}
            {#if row.note}<div class="row-note">{row.note}</div>{/if}
          </div>
          {#if row.buttons && row.buttons.length > 0}
            <div class="buttons">
              {#each row.buttons as button (button.id)}
                <Button size="compact" disabled={button.busy} aria-label={button.aria} onclick={button.onclick}>
                  {button.busy && button.busyLabel ? button.busyLabel : button.label}
                </Button>
              {/each}
            </div>
          {/if}
        </div>
        {#if row.result}
          <div class="result {row.result.ok ? 'ok' : 'bad'}" role="status">
            <Icon name={row.result.ok ? 'check' : 'alert'} size={16} stroke={2.2} />
            <span>{row.result.text}</span>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
  {#if children}{@render children()}{/if}
  {#if note}<p class="note">{note}</p>{/if}
</Panel>

<style>
  li {
    padding: 14px 0;
  }
  li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  .line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 10px 14px;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .buttons :global(.btn) {
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
    overflow-wrap: anywhere;
  }
  .status {
    margin-top: 3px;
    color: var(--text-3);
  }
  .detail {
    margin-top: 2px;
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 18px;
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  .row-note {
    margin-top: 2px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .result {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-top: 10px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-2);
  }
  .result :global(svg) {
    margin-top: 1px;
  }
  .result.bad {
    color: var(--danger);
  }
  .empty,
  .note {
    margin-top: 10px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
</style>
