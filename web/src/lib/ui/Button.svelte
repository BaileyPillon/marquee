<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /**
     * primary: accent fill. outline: light outline on transparent. quiet: a dim outline for
     * secondary actions inside cards. ghost: text only (Cancel). link: amber underlined text.
     */
    variant?: 'primary' | 'outline' | 'quiet' | 'ghost' | 'link';
    /** xs: 30px. compact: 34px. sm: 36px (tables and cards). md: 44px. lg: 48px (phone). xl: 56px (phone decisions). */
    size?: 'xs' | 'compact' | 'sm' | 'md' | 'lg' | 'xl';
    /** A square button that holds only an icon (give it an aria-label). */
    icon?: boolean;
    /** Fully rounded ends, for the one headline action of a page. */
    pill?: boolean;
    /** Render a link instead of a button. */
    href?: string;
    /** The link leaves Marquee: it opens in a new tab. */
    external?: boolean;
    type?: 'button' | 'submit';
    block?: boolean;
    disabled?: boolean;
    'aria-label'?: string;
    onclick?: (event: MouseEvent) => void;
    children: Snippet;
  }

  let {
    variant = 'outline',
    size = 'md',
    icon = false,
    pill = false,
    href,
    external = false,
    type = 'button',
    block = false,
    disabled = false,
    'aria-label': ariaLabel,
    onclick,
    children,
  }: Props = $props();
</script>

{#if href}
  <a
    class="btn {variant} {size}"
    class:block
    class:icon
    class:pill
    {href}
    target={external ? '_blank' : undefined}
    rel={external ? 'noopener noreferrer' : undefined}
    aria-label={ariaLabel}
    {onclick}
  >
    {@render children()}
  </a>
{:else}
  <button class="btn {variant} {size}" class:block class:icon class:pill {type} {disabled} aria-label={ariaLabel} {onclick}>
    {@render children()}
  </button>
{/if}

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    box-sizing: border-box;
    height: 44px;
    padding: 0 20px;
    border-radius: var(--radius-sm);
    font-family: var(--font-sans);
    font-size: 14px;
    font-weight: 600;
    line-height: 1;
    text-decoration: none;
    white-space: nowrap;
    cursor: pointer;
    transition: background-color 0.12s ease;
  }
  .xs {
    height: 30px;
    padding: 0 11px;
    font-size: 12.5px;
  }
  .compact {
    height: 34px;
    padding: 0 14px;
    font-size: 13px;
  }
  .sm {
    height: 36px;
    padding: 0 14px;
    font-size: 13px;
  }
  .lg {
    height: 48px;
    border-radius: var(--radius-lg);
    font-size: 16px;
  }
  .xl {
    height: 56px;
    border-radius: var(--radius-lg);
    font-size: 17px;
    font-weight: 700;
  }
  .icon {
    width: 44px;
    padding: 0;
  }
  .pill {
    padding: 0 22px 0 18px;
    border-radius: var(--radius-pill);
  }
  .block {
    display: flex;
    width: 100%;
  }
  .primary {
    border: 0;
    background: var(--accent);
    color: var(--on-accent);
    font-weight: 700;
  }
  .primary:hover {
    background: var(--accent-hover);
    color: var(--on-accent);
  }
  .outline {
    border: 1px solid var(--line);
    background: transparent;
    color: var(--text);
  }
  .outline:hover {
    background: var(--hover);
    color: var(--text);
  }
  .xl.outline {
    border-width: 1.5px;
    border-color: var(--line-strong);
  }
  .quiet {
    border: 1px solid var(--input-border);
    background: transparent;
    color: var(--text);
  }
  .quiet:hover {
    background: var(--hover);
    color: var(--text);
  }
  .ghost {
    border: 0;
    background: transparent;
    color: var(--text-2);
  }
  .ghost:hover {
    background: var(--hover);
    color: var(--text);
  }
  .link {
    height: auto;
    min-height: 44px;
    padding: 0 4px;
    border: 0;
    background: transparent;
    color: var(--accent);
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  .link:hover {
    color: var(--accent-hover);
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
