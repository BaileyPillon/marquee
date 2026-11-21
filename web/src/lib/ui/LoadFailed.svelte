<script lang="ts">
  import Button from './Button.svelte';
  import Panel from './Panel.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** Ask for the data again. */
    onretry: () => void;
  }

  let { onretry }: Props = $props();
</script>

<Panel tag="div" padding="20px">
  <p class="msg" role="alert">{t('load.failed')}</p>
  <div class="row"><Button size="sm" onclick={onretry}>{t('load.retry')}</Button></div>
</Panel>

<style>
  .msg {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-2);
  }
  .row {
    margin-top: 12px;
  }
</style>
