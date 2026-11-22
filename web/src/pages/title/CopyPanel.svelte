<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Badge from '../../lib/ui/Badge.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import Toast from '../../lib/ui/Toast.svelte';
  import { ApiError } from '../../lib/core/api';
  import { failureKey, isRouteMissing } from '../../lib/core/catalog';
  import { debounce, onEvent, onResync } from '../../lib/core/events';
  import { formatBytes, formatDay } from '../../lib/core/format';
  import { t } from '../../lib/core/i18n';
  import { requestsState } from '../../lib/core/requests.svelte.ts';
  import {
    approveRequest,
    declineRequest,
    listRequests,
    type RequestItem,
  } from '../../lib/core/requestsApi';
  import { askerFirst, decidedLine } from '../../lib/core/requestView';
  import { session } from '../../lib/core/session.svelte';
  import { listProfiles, type Profile } from '../../lib/core/sourcesApi';
  import { createToaster } from '../../lib/core/toast.svelte.ts';
  import DeclineDialog from '../requests/DeclineDialog.svelte';

  interface Props {
    titleId: number;
    kind: 'movie' | 'show';
    /** Open the ask sheet (a 4K ask). */
    onask: () => void;
  }

  let { titleId, kind, onask }: Props = $props();

  const uid = $props.id();
  const headId = `copy-${uid}`;
  const toaster = createToaster();

  const role = $derived(session.me?.role);
  const canAnswer = $derived(role === 'owner' || role === 'admin');

  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let status = $state<Status>('loading');
  let all = $state.raw<RequestItem[]>([]);
  let profile = $state.raw<Profile | undefined>();

  /**
   * The 4K asks for this title, newest first. The server only sends this title's; the check also
   * keeps the last title's asks off the screen for the moment after navigating to another.
   */
  const mine = $derived(all.filter((r) => r.title.id === titleId && r.quality === 'uhd'));
  // The one that matters: one still waiting, else one that is going through, else the latest.
  const current = $derived(
    mine.find((r) => r.state === 'pending') ??
      mine.find((r) => r.state === 'approved' || r.state === 'available' || r.state === 'partial') ??
      mine[0],
  );

  async function load(): Promise<void> {
    try {
      all = (await listRequests({ titleId, limit: 50, mine: !canAnswer })).items;
      status = 'ready';
    } catch (err) {
      if (isRouteMissing(err)) status = 'off';
      else if (status !== 'ready') status = 'failed';
    }
  }

  async function loadProfile(): Promise<void> {
    if (role !== 'owner') return;
    try {
      const profiles = await listProfiles();
      profile = profiles.find((p) => p.kind === kind && p.quality === 'uhd');
    } catch {
      profile = undefined; // the quality rules are not shown, nothing more
    }
  }

  $effect(() => {
    void titleId;
    void load();
  });

  onMount(() => {
    void loadProfile();
    const soon = debounce(() => void load(), 250);
    const stop = onEvent('requests', () => soon.call());
    const stopResync = onResync(() => soon.call());
    return () => {
      soon.cancel();
      stop();
      stopResync();
    };
  });

  // ---- words ----
  const rule = $derived.by(() => {
    const parts: string[] = [];
    if (profile) {
      const res = profile.preferredRes || Math.max(0, ...(profile.allowedRes ?? []));
      if (res) parts.push(`${res}p`);
      if (profile.preferHdr) parts.push(t('copy4k.hdr'));
      if (profile.maxSizeMb) parts.push(t('copy4k.under', { size: formatBytes(profile.maxSizeMb * 1024 * 1024) }));
    }
    const folder = current?.facts.folderPath;
    if (folder) parts.push(t('copy4k.folder', { folder }));
    return parts.length > 0 ? t('copy4k.rule', { rule: parts.join(' · ') }) : '';
  });

  const badge = $derived.by<{ tone: 'solid' | 'dashed' | 'inverse'; text: string } | undefined>(() => {
    if (!current) return undefined;
    switch (current.state) {
      case 'pending':
        return { tone: 'dashed', text: t('status.requested') };
      case 'available':
        return { tone: 'solid', text: t('status.library') };
      case 'declined':
      case 'failed':
        return { tone: 'inverse', text: current.state === 'failed' ? t('requests.failed') : t('requests.declined') };
      case 'cancelled':
        return { tone: 'dashed', text: t('requests.cancelled') };
      default:
        return { tone: 'solid', text: t('requests.approved') };
    }
  });

  const headline = $derived.by(() => {
    if (!current) return t('copy4k.none');
    const who = askerFirst(current);
    switch (current.state) {
      case 'pending':
        return canAnswer
          ? t('copy4k.pending.owner', { who })
          : t('copy4k.pending.member', { owner: requestsState.ownerName || t('requests.by.owner') });
      case 'approved':
        return t('copy4k.approved');
      case 'available':
        return t('copy4k.available');
      case 'partial':
        return t('copy4k.partial');
      case 'failed':
        return t('copy4k.failed');
      case 'declined':
        return t('copy4k.declined');
      default:
        return t('copy4k.cancelled');
    }
  });

  const sub = $derived.by(() => {
    if (!current) return t('copy4k.noneSub');
    if (current.state === 'pending') return t('copy4k.asked', { date: formatDay(current.createdAt) });
    return decidedLine(current);
  });

  const canAskAgain = $derived(
    !current || current.state === 'declined' || current.state === 'cancelled' || current.state === 'failed',
  );

  // ---- answering inline ----
  let busy = $state(false);
  let root: HTMLElement | undefined = $state();

  async function settleFocus(): Promise<void> {
    await tick();
    root?.focus({ preventScroll: true });
  }

  function failedText(err: unknown): string {
    return err instanceof ApiError ? t(failureKey(err)) : t('requests.toast.failed');
  }

  async function approve(always: boolean): Promise<void> {
    const request = current;
    if (!request || busy) return;
    busy = true;
    try {
      const next = await approveRequest(request.id, { alwaysAllowUHD: always });
      all = all.map((r) => (r.id === next.id ? next : r));
      void requestsState.refresh();
      toaster.show(
        t(always ? 'requests.toast.approvedAlways' : 'requests.toast.approved', {
          title: request.title.name,
          who: askerFirst(request),
        }),
      );
      await settleFocus();
    } catch (err) {
      toaster.show(failedText(err));
      void load();
    } finally {
      busy = false;
    }
  }

  let declineOpen = $state(false);
  let declineBusy = $state(false);
  let declineError = $state('');

  async function confirmDecline(reason: string): Promise<void> {
    const request = current;
    if (!request || declineBusy) return;
    declineBusy = true;
    declineError = '';
    try {
      const next = await declineRequest(request.id, reason);
      all = all.map((r) => (r.id === next.id ? next : r));
      void requestsState.refresh();
      declineOpen = false;
      toaster.show(t('requests.toast.declined', { title: request.title.name, who: askerFirst(request) }));
      await settleFocus();
    } catch (err) {
      declineError = failedText(err);
    } finally {
      declineBusy = false;
    }
  }
</script>

<div class="wrap" tabindex="-1" bind:this={root}>
  <Panel labelledby={headId}>
    <SectionHead title={t('title.copy4k.heading')} id={headId} align="center" gap={14}>
      {#if badge}<Badge tone={badge.tone} size="md">{badge.text}</Badge>{/if}
    </SectionHead>

    {#if status === 'loading'}
      <p class="asked" role="status">{t('load.loading')}</p>
    {:else if status === 'off'}
      <p class="asked">{t('copy4k.off')}</p>
    {:else if status === 'failed'}
      <p class="asked" role="alert">{t('load.failed')}</p>
    {:else}
      <p class="headline">{headline}</p>
      <p class="asked">{sub}</p>
      {#if current && current.state === 'declined' && current.declineReason}
        <p class="asked">{t('copy4k.reason', { reason: current.declineReason })}</p>
      {/if}

      <div class="decide">
        {#if current?.state === 'pending' && canAnswer}
          <Button
            variant="primary"
            disabled={busy}
            aria-label={t('requests.approveAria4k', { who: askerFirst(current), title: current.title.name })}
            onclick={() => approve(false)}
          >
            <Icon name="check" size={18} stroke={2.4} />
            {t('action.approve')}
          </Button>
          <Button
            disabled={busy}
            aria-label={t('requests.declineAria4k', { who: askerFirst(current), title: current.title.name })}
            onclick={() => {
              declineError = '';
              declineOpen = true;
            }}
          >
            <Icon name="close" size={18} stroke={2.4} />
            {t('action.decline')}
          </Button>
        {:else if canAskAgain}
          <Button variant={current ? 'outline' : 'primary'} onclick={onask}>
            {current ? t('copy4k.askAgain') : t('hero.askFor', { quality: t('ask.quality.uhd.short') })}
          </Button>
        {/if}
      </div>
      {#if current?.state === 'pending' && canAnswer}
        <div class="always">
          <Button variant="link" disabled={busy} onclick={() => approve(true)}>
            {t('requests.alwaysAllow', { who: askerFirst(current), quality: t('ask.quality.uhd.short') })}
          </Button>
        </div>
      {/if}

      {#if rule}
        <div class="rule">
          <span class="rule-icon"><Icon name="rules" size={18} /></span>
          <span>{rule}</span>
        </div>
      {/if}
    {/if}
  </Panel>
</div>

<DeclineDialog
  bind:open={declineOpen}
  request={current}
  busy={declineBusy}
  error={declineError}
  onconfirm={confirmDecline}
/>

<Toast message={toaster.message} />

<style>
  .wrap {
    outline: none;
    min-width: 0;
  }
  .wrap > :global(.panel) {
    height: 100%;
  }
  .headline {
    font-size: 15px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
  }
  .asked {
    margin-top: 2px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .decide {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 14px;
  }
  .decide:empty {
    display: none;
  }
  .always {
    margin-top: 4px;
  }
  .rule {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--hairline-strong);
    font-size: 13.5px;
    line-height: 20px;
    color: var(--text-3);
  }
  .rule-icon {
    display: flex;
    margin-top: 1px;
    color: var(--muted);
  }
</style>
