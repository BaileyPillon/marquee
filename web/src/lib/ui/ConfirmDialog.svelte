<script lang="ts">
  import BottomSheet from './BottomSheet.svelte';
  import Button from './Button.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    /** The question, e.g. "Remove Movies?". The dialog is labelled by it. */
    title: string;
    /** What will happen, in plain words. */
    body: string;
    /** The button that goes ahead, e.g. "Remove folder". */
    confirmLabel: string;
    /** The request is under way. */
    busy?: boolean;
    /** Why the last try failed. */
    error?: string;
    onconfirm: () => void;
    /** Called after the dialog has closed without going ahead. */
    onclose?: () => void;
  }

  let {
    open = $bindable(false),
    title,
    body,
    confirmLabel,
    busy = false,
    error = '',
    onconfirm,
    onclose,
  }: Props = $props();
</script>

<!-- A native modal dialog (BottomSheet): focus goes in, Tab stays inside, Esc and the cancel button close it, focus returns to the button that opened it. -->
<BottomSheet bind:open {title} {onclose}>
  <p class="body">{body}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <div class="actions">
    <Button variant="primary" size="lg" block disabled={busy} onclick={onconfirm}>
      {busy ? t('confirm.working') : confirmLabel}
    </Button>
    <Button variant="ghost" size="lg" block onclick={() => (open = false)}>{t('confirm.cancel')}</Button>
  </div>
</BottomSheet>

<style>
  .body {
    margin: 10px 0 0;
    font-size: 14.5px;
    line-height: 21px;
    color: var(--text-2);
  }
  .error {
    margin: 12px 0 0;
    font-size: 14px;
    line-height: 20px;
    color: var(--danger);
  }
  .actions {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 18px;
  }
</style>
