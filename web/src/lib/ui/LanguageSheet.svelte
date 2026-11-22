<script lang="ts">
  import BottomSheet from './BottomSheet.svelte';
  import LanguagePicker from './LanguagePicker.svelte';
  import { isDesktop } from '../core/media';
  import { t } from '../core/i18n';

  interface Props {
    /** Whether the sheet is showing. Bind it. */
    open: boolean;
    /** Called after the sheet has closed, whichever way it closed. */
    onclose?: () => void;
  }

  let { open = $bindable(false), onclose }: Props = $props();
</script>

<!-- The language list in a sheet: opened from the account menu on every page. -->
<BottomSheet bind:open title={t('lang.sheetTitle')} {onclose}>
  <div class="body">
    <LanguagePicker focusOnMount={$isDesktop} onchosen={() => (open = false)} />
  </div>
</BottomSheet>

<style>
  .body {
    margin-top: 14px;
  }
</style>
