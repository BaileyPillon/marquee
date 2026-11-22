<script module lang="ts">
  export interface DataColumn {
    id: string;
    label: string;
    align?: 'start' | 'end';
    /** Keep the header for assistive tech but do not show it (an actions column). */
    hideLabel?: boolean;
  }
</script>

<script lang="ts" generics="T">
  import type { Snippet } from 'svelte';

  interface Props {
    /** Names the table for assistive tech, and the scrolling region around it. */
    label: string;
    columns: DataColumn[];
    /** CSS grid-template-columns shared by the header and every row. */
    template: string;
    rows: T[];
    rowKey: (row: T) => string | number;
    /** The table keeps this width and scrolls sideways inside its box when the box is narrower. */
    minWidth: number;
    /**
     * loose: header and rows sit flush with the panel's padding, rows are ruled from the top.
     * inset: padded rows under a ruled header, for a table that fills its card.
     */
    variant?: 'loose' | 'inset';
    /** Side padding of the rows in px (inset only). */
    padX?: number;
    /** Minimum row height in px (inset only). */
    rowMin?: number;
    /** A softer line between rows. */
    softRule?: boolean;
    /** Room under the table for the scrollbar, in px. */
    padBottom?: number;
    /** Draws one cell. Switch on `column.id`. */
    cell: Snippet<[row: T, column: DataColumn]>;
  }

  let {
    label,
    columns,
    template,
    rows,
    rowKey,
    minWidth,
    variant = 'loose',
    padX = 16,
    rowMin = 52,
    softRule = false,
    padBottom = 0,
    cell,
  }: Props = $props();
</script>

<!--
  A box that scrolls sideways must be reachable from the keyboard, so it takes focus (WCAG 2.1.1);
  the region role and name tell a screen reader what it holds.
-->
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div
  class="scroller"
  role="region"
  aria-label={label}
  tabindex="0"
  style:--cols={template}
  style:--pad-x="{variant === 'inset' ? padX : 0}px"
  style:--row-min="{rowMin}px"
  style:padding-bottom="{padBottom}px"
>
  <div class="table {variant}" class:soft={softRule} role="table" aria-label={label} style:min-width="{minWidth}px">
    <div class="row head" role="row">
      {#each columns as column (column.id)}
        <div class="th" class:end={column.align === 'end'} role="columnheader">
          {#if column.hideLabel}<span class="sr-only">{column.label}</span>{:else}{column.label}{/if}
        </div>
      {/each}
    </div>
    {#each rows as row (rowKey(row))}
      <div class="row body" role="row">
        {#each columns as column (column.id)}
          <div class="td" class:end={column.align === 'end'} role="cell">
            {@render cell(row, column)}
          </div>
        {/each}
      </div>
    {/each}
  </div>
</div>

<style>
  .scroller {
    /* Contains the visually hidden header text: without it that text is positioned against the page
       and, sitting at the far end of a wide table, makes the whole page scroll sideways. */
    position: relative;
    overflow-x: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--input-border) transparent;
  }
  .row {
    display: grid;
    grid-template-columns: var(--cols);
    column-gap: 16px;
    box-sizing: border-box;
    padding-inline: var(--pad-x);
  }
  .th {
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .td {
    min-width: 0;
  }
  .end {
    text-align: end;
  }

  .loose .head {
    align-items: end;
    padding-bottom: 10px;
  }
  .loose .th {
    letter-spacing: 0.1em;
  }
  .loose .body {
    align-items: center;
    padding-top: 14px;
    padding-bottom: 14px;
    border-top: 1px solid var(--hairline-strong);
  }

  .inset .head {
    align-items: center;
    min-height: 40px;
    border-bottom: 1px solid var(--hairline-strong);
  }
  .inset .body {
    align-items: center;
    min-height: var(--row-min);
    padding-top: 12px;
    padding-bottom: 12px;
  }
  .inset .body + .body {
    border-top: 1px solid var(--hairline-strong);
  }
  .inset.soft .body + .body {
    border-color: var(--hairline);
  }
</style>
