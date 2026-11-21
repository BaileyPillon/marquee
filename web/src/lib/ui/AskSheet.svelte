<script lang="ts">
  import AskForm from './AskForm.svelte';
  import BottomSheet from './BottomSheet.svelte';
  import Toast from './Toast.svelte';
  import type { ApiKind, AskQuality, AskTarget } from '../core/catalog';
  import { t } from '../core/i18n';
  import { createToaster } from '../core/toast.svelte.ts';

  interface Props {
    /** Whether the sheet is showing. Bind it. */
    open: boolean;
    /** What is being asked for, e.g. "Metropolis". */
    title: string;
    /** Which title: its id in the catalog, or the provider's id for one that is not saved yet. */
    target: AskTarget;
    kind: ApiKind;
    /** The quality that starts selected. */
    defaultQuality?: AskQuality;
    /** The library already has it in HD (when known): only the 4K copy is offered. */
    haveHd?: boolean;
  }

  let {
    open = $bindable(false),
    title,
    target,
    kind,
    defaultQuality = 'hd',
    haveHd = false,
  }: Props = $props();

  // The toast lives here, not in the form: the form is gone once the sheet has closed.
  const toaster = createToaster();

  function sent(autoApproved: boolean): void {
    open = false;
    toaster.show(autoApproved ? t('ask.sentApproved', { title }) : t('ask.sentWaiting'));
  }
</script>

<BottomSheet bind:open title={t('ask.title', { title })}>
  <AskForm {target} {kind} {defaultQuality} {haveHd} onsent={sent} oncancel={() => (open = false)} />
</BottomSheet>

<Toast message={toaster.message} />
