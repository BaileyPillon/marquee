<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /** md: the desktop call to action (52px). lg: the phone one (56px, rounder). */
    size?: 'md' | 'lg';
    /** A request is under way: the button looks pressed and ignores further clicks. */
    busy?: boolean;
    disabled?: boolean;
    children: Snippet;
  }

  let { size = 'md', busy = false, disabled = false, children }: Props = $props();
</script>

<!-- aria-disabled (not disabled) while busy, so keyboard focus stays on the button. -->
<button
  type="submit"
  class="cta {size}"
  class:busy
  {disabled}
  aria-disabled={busy ? 'true' : undefined}
  onclick={(event) => {
    if (busy) event.preventDefault();
  }}
>
  {@render children()}
</button>

<style>
  .cta {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    box-sizing: border-box;
    width: 100%;
    min-height: 52px;
    margin-top: 6px;
    padding: 0 20px;
    border: 0;
    border-radius: var(--radius-sm);
    background: var(--accent);
    color: var(--on-accent);
    font-family: var(--font-sans);
    font-size: 15px;
    font-weight: 700;
    line-height: 1;
    cursor: pointer;
    transition: background-color 0.12s ease;
  }
  .cta.lg {
    min-height: 56px;
    margin-top: 8px;
    border-radius: var(--radius-lg);
    font-size: 17px;
  }
  .cta:hover {
    background: var(--accent-hover);
  }
  .cta.busy {
    cursor: progress;
    opacity: 0.75;
  }
  .cta:disabled {
    background: var(--hairline-strong);
    color: var(--text-3);
    cursor: not-allowed;
  }
</style>
