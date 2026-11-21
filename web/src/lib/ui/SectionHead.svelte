<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    /** For aria-labelledby on the section this heads. */
    id?: string;
    /** lg 18px (sections), md 16px (settings and side panels). */
    size?: 'lg' | 'md';
    /** baseline: text beside it (a note, "See all"). center: a button or badge beside it. */
    align?: 'baseline' | 'center';
    /** Space under the heading, in px. */
    gap?: number;
    /** Least height of the row in px, so a panel with a button and one without line up. */
    minHeight?: number;
    /** What sits at the right of the heading. */
    children?: Snippet;
  }

  let { title, id, size = 'lg', align = 'baseline', gap = 10, minHeight, children }: Props = $props();
</script>

<div
  class="head {align}"
  style:margin-bottom="{gap}px"
  style:min-height={minHeight ? `${minHeight}px` : undefined}
>
  <h2 {id} class={size}>{title}</h2>
  {#if children}{@render children()}{/if}
</div>

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
  }
  .baseline {
    align-items: baseline;
    gap: 4px 16px;
  }
  .center {
    align-items: center;
    gap: 8px 12px;
  }
  h2 {
    font-weight: 600;
    color: var(--text);
  }
  .lg {
    font-size: 18px;
    line-height: 24px;
  }
  .md {
    font-size: 16px;
    line-height: 22px;
  }
</style>
