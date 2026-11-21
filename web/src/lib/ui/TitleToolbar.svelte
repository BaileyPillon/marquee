<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Toast from './Toast.svelte';
  import Toggle from './Toggle.svelte';
  import { failureKey, refreshTitle, setMonitored, type TitleDetail } from '../core/catalog';
  import { t } from '../core/i18n';
  import { session } from '../core/session.svelte';
  import { createToaster } from '../core/toast.svelte.ts';

  interface Props {
    detail: TitleDetail;
    /** Names the group of buttons, e.g. "Actions for Metropolis". */
    label: string;
    /** The monitor switch's words, e.g. "Monitor" or "Watch for new episodes". */
    monitorLabel: string;
    /** The ask button's words; left out, there is no ask button. */
    askLabel?: string;
    onask?: () => void;
    /** A save answered with the fresh copy of the title. */
    onupdated: (detail: TitleDetail) => void;
  }

  let { detail, label, monitorLabel, askLabel, onask, onupdated }: Props = $props();

  // Watching and refreshing are for the owner and admins; anyone signed in may ask.
  const canManage = $derived(session.me?.role === 'owner' || session.me?.role === 'admin');

  const toaster = createToaster();
  let pending = $state<boolean | undefined>();
  const monitored = $derived(pending ?? detail.monitored);
  let refreshing = $state(false);

  async function toggle(next: boolean): Promise<void> {
    if (pending !== undefined) return;
    pending = next;
    try {
      onupdated(await setMonitored(detail.id, next));
    } catch (err) {
      toaster.show(t(failureKey(err)));
    } finally {
      pending = undefined;
    }
  }

  async function refresh(): Promise<void> {
    if (refreshing) return;
    refreshing = true;
    try {
      onupdated(await refreshTitle(detail.id));
      toaster.show(t('toolbar.refreshed'));
    } catch (err) {
      toaster.show(t(failureKey(err)));
    } finally {
      refreshing = false;
    }
  }
</script>

<div class="actions" role="group" aria-label={label}>
  {#if askLabel && onask}
    <Button variant="primary" onclick={onask}>{askLabel}</Button>
  {/if}
  {#if canManage}
    <Toggle checked={monitored} label={monitorLabel} onchange={toggle} />
  {/if}
  {#if detail.trailerKey}
    <Button
      external
      href="https://www.youtube.com/watch?v={encodeURIComponent(detail.trailerKey)}"
      aria-label={t('toolbar.trailerAria')}
    >
      <Icon name="trailer" size={18} stroke={2} />
      {t('title.trailer')}
    </Button>
  {/if}
  {#if canManage}
    <Button disabled={refreshing} onclick={refresh}>
      <Icon name="refresh" size={18} stroke={2} />
      {refreshing ? t('toolbar.refreshing') : t('toolbar.refresh')}
    </Button>
  {/if}
</div>

<Toast message={toaster.message} />

<style>
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    margin-bottom: 18px;
  }
</style>
