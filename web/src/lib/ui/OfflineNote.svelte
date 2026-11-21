<script lang="ts">
  import { t } from '../core/i18n';

  interface Props {
    offline: boolean;
  }

  let { offline }: Props = $props();
</script>

<!-- The status region stays in the page so that screen readers announce when it fills. -->
<div class="note" role="status">
  {#if offline}
    <span class="pill"><span class="dot" aria-hidden="true"></span>{t('health.offline')}</span>
  {/if}
</div>

<style>
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    box-sizing: border-box;
    padding: 3px 10px;
    border-radius: var(--radius-pill);
    border: 1px dashed var(--line-dashed);
    background: var(--scrim);
    color: var(--text);
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    white-space: nowrap;
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent);
  }
</style>
