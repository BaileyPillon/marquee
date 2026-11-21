<script lang="ts">
  import AskForm from './AskForm.svelte';
  import BottomSheet from './BottomSheet.svelte';
  import Toast from './Toast.svelte';
  import { t } from '../core/i18n';
  import { createToaster } from '../core/toast.svelte.ts';

  interface Props {
    /** Whether the sheet is showing. Bind it. */
    open: boolean;
    /** What is being asked for, e.g. "Metropolis". */
    title: string;
    /** Which title, for the options the API will return. */
    titleId: string;
  }

  let { open = $bindable(false), title, titleId }: Props = $props();

  // The toast lives here, not in the form: the form is gone once the sheet has closed.
  const toaster = createToaster();

  function sent(owner: string): void {
    open = false;
    toaster.show(t('ask.sent', { owner }));
  }
</script>

<BottomSheet bind:open title={t('ask.title', { title })}>
  <AskForm {titleId} onsend={sent} oncancel={() => (open = false)} />
</BottomSheet>

<Toast message={toaster.message} />
