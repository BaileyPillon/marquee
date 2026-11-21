<script lang="ts">
  import Field from '../../lib/accounts/Field.svelte';
  import FormSheet from '../../lib/ui/FormSheet.svelte';
  import { t } from '../../lib/core/i18n';
  import type { RequestItem } from '../../lib/core/requestsApi';
  import { askerFirst } from '../../lib/core/requestView';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    /** The ask being declined. */
    request: RequestItem | undefined;
    busy: boolean;
    error: string;
    /** Called with the reason (maybe empty) when the owner confirms. */
    onconfirm: (reason: string) => void;
    onclose?: () => void;
  }

  let { open = $bindable(false), request, busy, error, onconfirm, onclose }: Props = $props();

  let reason = $state('');
  // A fresh, empty reason every time the dialog opens.
  $effect(() => {
    if (open) reason = '';
  });
</script>

<FormSheet
  bind:open
  title={t('requests.decline.title', { title: request?.title.name ?? '' })}
  intro={request ? t('requests.decline.intro', { who: askerFirst(request) }) : undefined}
  submitLabel={t('requests.decline.confirm')}
  {busy}
  {error}
  {onclose}
  onsubmit={() => onconfirm(reason)}
>
  <Field
    label={t('requests.decline.reason')}
    bind:value={reason}
    name="reason"
    help={t('requests.decline.reasonHelp')}
  />
</FormSheet>
