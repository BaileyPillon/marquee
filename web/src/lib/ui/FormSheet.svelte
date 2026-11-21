<script lang="ts">
  import type { Snippet } from 'svelte';
  import ErrorNote from '../accounts/ErrorNote.svelte';
  import BottomSheet from './BottomSheet.svelte';
  import Button from './Button.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    title: string;
    /** The button that saves, e.g. "Save". */
    submitLabel: string;
    /** The request is under way. */
    busy?: boolean;
    /** Why the last try failed. */
    error?: string;
    /** Plain words under the heading. */
    intro?: string;
    /** Runs when the form is sent (Enter, or the submit button). */
    onsubmit: () => void;
    /** Called after the dialog has closed without saving. */
    onclose?: () => void;
    /** A wider dialog on desktop. */
    wide?: boolean;
    /** Extra buttons beside Cancel (a Test button, say). */
    extra?: Snippet;
    children: Snippet;
  }

  let {
    open = $bindable(false),
    title,
    submitLabel,
    busy = false,
    error = '',
    intro,
    onsubmit,
    onclose,
    wide = false,
    extra,
    children,
  }: Props = $props();

  function send(event: SubmitEvent): void {
    event.preventDefault();
    if (!busy) onsubmit();
  }
</script>

<!-- A native modal dialog (BottomSheet): focus goes in, Tab stays inside, Esc and Cancel close it, focus returns to the button that opened it. -->
<BottomSheet bind:open {title} {onclose} {wide}>
  {#if intro}<p class="intro">{intro}</p>{/if}
  <form class="form" onsubmit={send} novalidate>
    {@render children()}
    {#if error}<div class="error"><ErrorNote>{error}</ErrorNote></div>{/if}
    <div class="actions">
      <Button variant="primary" size="lg" type="submit" block disabled={busy}>
        {busy ? t('confirm.working') : submitLabel}
      </Button>
      {#if extra}{@render extra()}{/if}
      <Button variant="ghost" size="lg" block onclick={() => (open = false)}>{t('confirm.cancel')}</Button>
    </div>
  </form>
</BottomSheet>

<style>
  .intro {
    margin: 8px 0 0;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .form {
    display: flex;
    flex-direction: column;
    gap: 14px;
    margin-top: 16px;
  }
  .error {
    margin-top: 2px;
  }
  .actions {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 4px;
  }
</style>
