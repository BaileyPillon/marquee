<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /** primary: accent fill. outline: light outline on transparent. */
    variant?: 'primary' | 'outline';
    /** md: 44px. sm: 36px (tables and cards). lg: 48px (phone). */
    size?: 'sm' | 'md' | 'lg';
    /** Render a link instead of a button. */
    href?: string;
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
    href,
    type = 'button',
    block = false,
    disabled = false,
    'aria-label': ariaLabel,
    onclick,
    children,
  }: Props = $props();
</script>

{#if href}
  <a class="btn {variant} {size}" class:block {href} aria-label={ariaLabel} {onclick}>
    {@render children()}
  </a>
{:else}
  <button class="btn {variant} {size}" class:block {type} {disabled} aria-label={ariaLabel} {onclick}>
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
  .btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
