<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * solid: outlined on a dark scrim (In library, Downloading, Approved).
   * dashed: dashed outline (Requested).
   * inverse: filled light (Missing, Declined).
   * count: the round number on a nav item.
   */
  type Tone = 'solid' | 'dashed' | 'inverse' | 'count';

  interface Props {
    tone?: Tone;
    size?: 'xs' | 'sm' | 'md';
    children: Snippet;
  }

  let { tone = 'solid', size = 'sm', children }: Props = $props();
</script>

<span class="badge {tone} {size}">{@render children()}</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    box-sizing: border-box;
    border-radius: var(--radius-pill);
    font-family: var(--font-sans);
    font-weight: 600;
    white-space: nowrap;
  }
  .xs {
    padding: 2px 7px;
    font-size: 11px;
    line-height: 14px;
  }
  .sm {
    padding: 3px 8px;
    font-size: 11px;
    line-height: 14px;
  }
  .md {
    padding: 3px 10px;
    font-size: 11.5px;
    line-height: 14px;
  }
  .solid {
    background: var(--scrim);
    color: var(--text);
    border: 1px solid var(--line-strong);
  }
  .dashed {
    background: var(--scrim);
    color: var(--text);
    border: 1px dashed var(--line-dashed);
  }
  .inverse {
    background: var(--text);
    color: var(--ground);
    border: 1px solid var(--text);
  }
  .count {
    justify-content: center;
    min-width: 22px;
    height: 22px;
    padding: 0 6px;
    border-radius: 11px;
    background: var(--text);
    color: var(--ground);
    font-size: 12px;
    line-height: 1;
    font-weight: 700;
  }
</style>
