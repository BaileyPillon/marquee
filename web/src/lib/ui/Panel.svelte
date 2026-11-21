<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /** A landmark when it has a name: use aria-label or aria-labelledby, not both. */
    label?: string;
    labelledby?: string;
    tag?: 'section' | 'article' | 'div';
    /** CSS padding. The canvas uses 18px 20px for most panels. */
    padding?: string;
    /** md 8px (list rows), lg 10px (panels), xl 14px (phone cards). */
    radius?: 'md' | 'lg' | 'xl';
    /** Clip children to the rounded corners (a table that reaches the edges). */
    clip?: boolean;
    children: Snippet;
  }

  let {
    label,
    labelledby,
    tag = 'section',
    padding = '18px 20px',
    radius = 'lg',
    clip = false,
    children,
  }: Props = $props();
</script>

<svelte:element
  this={tag}
  class="panel {radius}"
  class:clip
  style:padding
  aria-label={label}
  aria-labelledby={labelledby}
>
  {@render children()}
</svelte:element>

<style>
  .panel {
    box-sizing: border-box;
    min-width: 0;
    background: var(--panel);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
  }
  .md {
    border-radius: var(--radius-md);
  }
  .lg {
    border-radius: var(--radius-lg);
  }
  .xl {
    border-radius: var(--radius-xl);
  }
  .clip {
    overflow: hidden;
  }
</style>
