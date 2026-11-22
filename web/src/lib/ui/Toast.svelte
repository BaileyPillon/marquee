<script lang="ts">
  import Icon from './Icon.svelte';

  interface Props {
    /** The message to show, or null for none. Drive it with createToaster(). */
    message: string | null;
  }

  let { message }: Props = $props();
</script>

<!-- The live region stays in the page so that screen readers announce a message when it appears. -->
<div class="region" role="status" aria-live="polite" aria-atomic="true">
  {#if message}
    <div class="toast">
      <Icon name="check" size={18} stroke={2.4} />
      <span>{message}</span>
    </div>
  {/if}
</div>

<style>
  .region {
    position: fixed;
    inset-inline: 16px;
    bottom: calc(var(--tabbar-height) + env(safe-area-inset-bottom) + 16px);
    z-index: 60;
    display: flex;
    justify-content: center;
    pointer-events: none;
  }
  .toast {
    display: flex;
    align-items: center;
    gap: 10px;
    box-sizing: border-box;
    max-width: 100%;
    padding: 12px 16px;
    border-radius: var(--radius-lg);
    background: var(--panel);
    box-shadow:
      inset 0 0 0 1px var(--line-strong),
      0 8px 24px rgba(0, 0, 0, 0.45);
    color: var(--text);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    animation: toast-in 0.18s ease-out;
  }
  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
  }

  @media (min-width: 900px) {
    .region {
      bottom: 24px;
    }
  }
</style>
