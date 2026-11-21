<script lang="ts">
  import { t } from '../core/i18n';

  interface Props {
    /** Say more than "Not set up yet", e.g. what will switch it on. */
    hint?: string;
  }

  let { hint }: Props = $props();
</script>

<!-- A part whose backend does not answer yet: a quiet note, never an error. -->
<p class="note" role="status">
  <span class="title">{t('notSetUp.title')}</span>
  {#if hint}<span class="hint">{hint}</span>{/if}
</p>

<style>
  .note {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .title {
    font-weight: 600;
    color: var(--text-2);
  }
  .hint {
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
</style>
