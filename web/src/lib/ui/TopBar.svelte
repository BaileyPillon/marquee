<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import SearchField from './SearchField.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** The "Ask for…" button. */
    onask?: () => void;
  }

  let { onask }: Props = $props();
</script>

<div class="topbar">
  <div class="field"><SearchField /></div>
  <Button onclick={onask}>
    <Icon name="plus" size={18} stroke={2} />
    {t('action.ask')}
  </Button>
</div>

<style>
  .topbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 16px;
  }
  .field {
    flex: 1 1 320px;
    max-width: 520px;
  }
</style>
