<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /**
     * solid: light dot (working, connected). accent: amber dot (importing). hollow: an empty ring
     * (queued, not set up). The shape differs as well as the colour, and the text always says it.
     */
    kind?: 'solid' | 'accent' | 'hollow';
    /** The status in words. Required: a dot alone is never a status. */
    children: Snippet;
  }

  let { kind = 'solid', children }: Props = $props();
</script>

<span class="status">
  <span class="dot {kind}" aria-hidden="true"></span>
  <span class="text">{@render children()}</span>
</span>

<style>
  /* Colour comes from the parent, so the same dot serves the cards (light text) and rows (soft text). */
  .status {
    display: inline-flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
  }
  .dot {
    flex: 0 0 auto;
    box-sizing: border-box;
    width: 8px;
    height: 8px;
    margin-top: 5px;
    border-radius: 50%;
  }
  .solid {
    background: var(--text);
  }
  .accent {
    background: var(--accent);
  }
  .hollow {
    background: transparent;
    border: 1.5px solid var(--muted);
  }
</style>
