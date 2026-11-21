<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * line: outlined (genres, formats). quiet: a thin ring (cast and crew). solid / dashed: the status
   * outlines used all over the app. inverse: filled light. faint: a dim tag. allowed / denied: a
   * permission the owner has ticked or not.
   */
  type Tone = 'line' | 'quiet' | 'solid' | 'dashed' | 'inverse' | 'faint' | 'allowed' | 'denied';

  /**
   * micro: 1px 8px tag. tag: 2px 8px tag. format: a small square-cornered label (FLAC).
   * status: 3px 9px status pill. value: 1px 10px pill that sits in a list row.
   * lg: 28px high link or permission chip. xl: 32px high link chip.
   */
  type Size = 'micro' | 'tag' | 'format' | 'status' | 'value' | 'lg' | 'xl';

  interface Props {
    tone?: Tone;
    size?: Size;
    /** Render a link. */
    href?: string;
    /** A dark backing, so the outline stays readable over a poster colour. */
    scrim?: boolean;
    /** A glyph before the text. */
    icon?: Snippet;
    children: Snippet;
  }

  let { tone = 'line', size = 'lg', href, scrim = false, icon, children }: Props = $props();
</script>

<svelte:element
  this={href ? 'a' : 'span'}
  class="chip {tone} {size}"
  class:with-icon={!!icon}
  class:scrim
  {href}
>
  {#if icon}{@render icon()}{/if}
  {@render children()}
</svelte:element>

<style>
  .chip {
    display: inline-flex;
    align-items: center;
    box-sizing: border-box;
    border-radius: var(--radius-pill);
    font-family: var(--font-sans);
    white-space: nowrap;
    color: var(--text);
    text-decoration: none;
  }
  a.chip:hover {
    background: var(--hover);
    color: var(--text);
  }

  .scrim {
    background: var(--scrim);
  }
  .line {
    border: 1px solid var(--line-chip);
  }
  .quiet {
    box-shadow: inset 0 0 0 1px var(--input-border);
  }
  .solid {
    border: 1px solid var(--line-strong);
  }
  .dashed {
    border: 1px dashed var(--line-dashed);
  }
  .inverse {
    background: var(--text);
    color: var(--ground);
    border: 1px solid var(--text);
  }
  .faint {
    border: 1px solid var(--line-faint);
    color: var(--text-3);
  }
  .allowed {
    border: 1px solid var(--line-strong);
    background: var(--hover);
    font-weight: 600;
  }
  .denied {
    border: 1px dashed var(--line-dim);
    color: var(--text-3);
    font-weight: 500;
  }

  .micro {
    padding: 1px 8px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
  }
  .tag {
    padding: 2px 8px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
  }
  .format {
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11.5px;
    font-weight: 600;
    line-height: 16px;
    letter-spacing: 0.06em;
  }
  .status {
    gap: 5px;
    padding: 3px 9px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
  }
  .value {
    padding: 1px 10px;
    font-size: 12px;
    font-weight: 600;
    line-height: 18px;
  }
  .lg {
    gap: 6px;
    height: 28px;
    padding: 0 12px;
    font-size: 12.5px;
    font-weight: 500;
    line-height: 1;
  }
  .lg.with-icon {
    padding: 0 11px 0 9px;
  }
  .xl {
    gap: 6px;
    height: 32px;
    padding: 0 13px;
    font-size: 13px;
    font-weight: 500;
    line-height: 1;
  }

  /* The weight of a permission chip follows its tone, whatever the size says. */
  .allowed.lg {
    font-weight: 600;
  }
  .denied.lg {
    font-weight: 500;
  }
</style>
