<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Button from '../lib/ui/Button.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import NotSetUp from '../lib/ui/NotSetUp.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import Toast from '../lib/ui/Toast.svelte';
  import { ApiError } from '../lib/core/api';
  import { failureKey, isRouteMissing } from '../lib/core/catalog';
  import { debounce, onEvent, onResync } from '../lib/core/events';
  import { t } from '../lib/core/i18n';
  import { libraryIndex } from '../lib/core/libraryIndex.svelte.ts';
  import { isDesktop } from '../lib/core/media';
  import { peopleState } from '../lib/core/people.svelte';
  import { requestsState } from '../lib/core/requests.svelte.ts';
  import {
    approveRequest,
    cancelRequest,
    declineRequest,
    listRequests,
    type RequestItem,
  } from '../lib/core/requestsApi';
  import { askerFirst } from '../lib/core/requestView';
  import { session } from '../lib/core/session.svelte';
  import { createToaster } from '../lib/core/toast.svelte.ts';
  import DeclineDialog from './requests/DeclineDialog.svelte';
  import DecidedList from './requests/DecidedList.svelte';
  import PeoplePanel from './requests/PeoplePanel.svelte';
  import PhoneApproval from './requests/PhoneApproval.svelte';
  import RequestCard from './requests/RequestCard.svelte';
  import WatchlistSummary from './requests/WatchlistSummary.svelte';

  const role = $derived(session.me?.role);
  const isStaff = $derived(role === 'owner' || role === 'admin');
  const isOwner = $derived(role === 'owner');

  const toaster = createToaster();

  // ---- loading: the whole list once, then again whenever the server says something changed ----
  type Status = 'loading' | 'ready' | 'failed' | 'off';
  let items = $state.raw<RequestItem[]>([]);
  let status = $state<Status>('loading');

  async function load(): Promise<void> {
    try {
      const res = await listRequests({ limit: 200, mine: !isStaff });
      items = res.items;
      status = 'ready';
    } catch (err) {
      if (isRouteMissing(err)) status = 'off';
      else if (status !== 'ready') status = 'failed';
    }
  }

  onMount(() => {
    void load();
    libraryIndex.ensure();
    if (isStaff) void peopleState.reload();
    const soon = debounce(() => {
      void load();
      if (isStaff) void peopleState.reload();
    }, 200);
    const stop = onEvent('requests', () => soon.call());
    const stopResync = onResync(() => soon.call());
    return () => {
      soon.cancel();
      stop();
      stopResync();
    };
  });

  /** Swap one request for its fresh copy (an answer from the server) without waiting for the stream. */
  function replace(next: RequestItem): void {
    items = items.map((r) => (r.id === next.id ? next : r));
  }

  // ---- what to show ----
  const waiting = $derived(items.filter((r) => r.state === 'pending'));
  const decided = $derived(
    items
      .filter((r) => r.state !== 'pending')
      .sort(
        (a, b) =>
          Date.parse(b.decidedAt ?? b.updatedAt) - Date.parse(a.decidedAt ?? a.updatedAt),
      ),
  );
  const WEEK_MS = 7 * 86_400_000;
  const decidedThisWeek = $derived(
    decided.filter((r) => Date.now() - Date.parse(r.decidedAt ?? r.updatedAt) < WEEK_MS).length,
  );

  let tab = $state('waiting');
  const tabs = $derived<TabItem[]>([
    { id: 'waiting', label: t('requests.tab.waiting', { n: waiting.length }) },
    { id: 'approved', label: t('requests.tab.approved') },
    { id: 'declined', label: t('requests.tab.declined') },
    { id: 'watchlists', label: t('requests.tab.watchlists') },
    { id: 'all', label: t('requests.tab.all') },
  ]);

  const showWaiting = $derived(tab === 'waiting' || tab === 'all');
  const shownDecided = $derived(
    tab === 'approved'
      ? decided.filter((d) => d.state === 'approved' || d.state === 'available' || d.state === 'partial')
      : tab === 'declined'
        ? decided.filter((d) => d.state === 'declined')
        : tab === 'watchlists'
          ? items.filter((d) => d.source === 'watchlist')
          : decided,
  );
  const decidedHeading = $derived(
    tab === 'approved'
      ? t('requests.tab.approved')
      : tab === 'declined'
        ? t('requests.tab.declined')
        : tab === 'watchlists'
          ? t('requests.tab.watchlists')
          : t('requests.recent'),
  );
  const summary = $derived(
    t('requests.summary', { waiting: waiting.length, decided: decidedThisWeek }),
  );

  // ---- answering ----
  let busy = $state<number | null>(null);
  let root: HTMLElement | undefined = $state();

  /** The button that was pressed is gone with its card: keep keyboard focus on the page. */
  async function settleFocus(): Promise<void> {
    await tick();
    root?.focus({ preventScroll: true });
  }

  function failedText(err: unknown): string {
    if (err instanceof ApiError && (err.code === 'not_pending' || err.code === 'not_cancellable')) {
      return t(failureKey(err));
    }
    return t('requests.toast.failed');
  }

  async function approve(request: RequestItem, alwaysAllow = false): Promise<void> {
    if (busy !== null) return;
    busy = request.id;
    try {
      replace(await approveRequest(request.id, { alwaysAllowUHD: alwaysAllow }));
      void requestsState.refresh();
      toaster.show(
        t(alwaysAllow ? 'requests.toast.approvedAlways' : 'requests.toast.approved', {
          title: request.title.name,
          who: askerFirst(request),
        }),
      );
      await settleFocus();
    } catch (err) {
      toaster.show(failedText(err));
      void load();
    } finally {
      busy = null;
    }
  }

  // Declining asks for an optional reason first, in a small dialog.
  let declineTarget = $state<RequestItem | undefined>();
  let declineOpen = $state(false);
  let declineBusy = $state(false);
  let declineError = $state('');

  function askDecline(request: RequestItem): void {
    declineTarget = request;
    declineError = '';
    declineOpen = true;
  }

  async function confirmDecline(reason: string): Promise<void> {
    const request = declineTarget;
    if (!request || declineBusy) return;
    declineBusy = true;
    declineError = '';
    try {
      replace(await declineRequest(request.id, reason));
      void requestsState.refresh();
      declineOpen = false;
      toaster.show(
        t('requests.toast.declined', { title: request.title.name, who: askerFirst(request) }),
      );
      await settleFocus();
    } catch (err) {
      declineError = failedText(err);
      if (err instanceof ApiError && err.code === 'not_pending') void load();
    } finally {
      declineBusy = false;
    }
  }

  // ---- a member's own asks ----
  let cancelling = $state<number | null>(null);

  async function cancel(request: RequestItem): Promise<void> {
    if (cancelling !== null) return;
    cancelling = request.id;
    try {
      replace(await cancelRequest(request.id));
      void requestsState.refresh();
      toaster.show(t('requests.toast.cancelled', { title: request.title.name }));
    } catch (err) {
      toaster.show(failedText(err));
      void load();
    } finally {
      cancelling = null;
    }
  }

  // ---- the phone's big card ----
  let activeId = $state<number | undefined>();
  const active = $derived(waiting.find((r) => r.id === activeId) ?? waiting[0]);

  const uid = $props.id();
  const waitingId = `req-waiting-${uid}`;
  const decidedId = `req-decided-${uid}`;
  const mineId = `req-mine-${uid}`;
</script>

<div class="page" tabindex="-1" bind:this={root}>
  {#if status === 'off'}
    {#if $isDesktop}<PageHeader title={t('route.requests')} />{/if}
    <NotSetUp hint={t('requests.offHint')} />
  {:else if status === 'ready'}
    {#if !isStaff}
      <!-- A member: their own asks, and Cancel on the ones still waiting. -->
      {#if $isDesktop}
        <div class="head">
          <PageHeader
            title={t('requests.mine')}
            subtitle={t('requests.mine.summary', { waiting: waiting.length })}
          />
        </div>
      {:else}
        <header class="phone-head">
          <h1>{t('requests.mine')}</h1>
          <p>{t('requests.mine.summary', { waiting: waiting.length })}</p>
        </header>
      {/if}
      <section aria-labelledby={mineId}>
        <h2 id={mineId} class="sr-only">{t('requests.mine')}</h2>
        {#if items.length > 0}
          <DecidedList {items} oncancel={cancel} busyId={cancelling} />
        {:else}
          <p class="empty">{t('requests.mine.empty')}</p>
          <div class="empty-action"><Button href="/explore">{t('library.empty.link')}</Button></div>
        {/if}
      </section>
    {:else if $isDesktop}
      <div class="head">
        <PageHeader title={t('route.requests')} subtitle={summary} />
      </div>

      <div class="tabs">
        <Tabs
          items={tabs}
          value={tab}
          onchange={(id) => (tab = id)}
          label={t('requests.filters')}
          prefix="requests"
        />
      </div>

      <div class="layout">
        <div
          class="results"
          role="tabpanel"
          id="requests-panel"
          aria-labelledby="requests-tab-{tab}"
        >
          {#if showWaiting}
            <section class="block" aria-labelledby={waitingId}>
              <SectionHead title={t('requests.waiting')} id={waitingId} gap={12} />
              {#if waiting.length > 0}
                <div class="cards">
                  {#each waiting as request (request.id)}
                    <RequestCard
                      {request}
                      busy={busy === request.id}
                      onapprove={() => approve(request)}
                      ondecline={() => askDecline(request)}
                      onalways={() => approve(request, true)}
                    />
                  {/each}
                </div>
              {:else}
                <p class="empty">{t('requests.phone.none')}</p>
              {/if}
            </section>
          {/if}

          <section aria-labelledby={decidedId}>
            <SectionHead title={decidedHeading} id={decidedId} gap={12}>
              {#if tab === 'waiting'}
                <button type="button" class="soft-link" onclick={() => (tab = 'all')}>
                  {t('action.seeAll')}
                </button>
              {/if}
            </SectionHead>
            {#if shownDecided.length > 0}
              <DecidedList items={tab === 'waiting' ? shownDecided.slice(0, 6) : shownDecided} />
            {:else}
              <p class="empty">{t('requests.empty')}</p>
            {/if}
          </section>
        </div>

        <div class="side">
          <PeoplePanel canEdit={isOwner} onsaved={() => toaster.show(t('limits.saved'))} />
          <WatchlistSummary canCheck={isOwner} onnote={(m) => toaster.show(m)} />
        </div>
      </div>
    {:else}
      <header class="phone-head">
        <h1>{t('route.requests')}</h1>
        <p>{t('requests.phone.summary', { waiting: waiting.length })}</p>
      </header>

      {#if active}
        <PhoneApproval
          {active}
          others={waiting.filter((r) => r.id !== active.id)}
          busy={busy === active.id}
          onapprove={() => approve(active)}
          ondecline={() => askDecline(active)}
          onalways={() => approve(active, true)}
          onpick={(id) => (activeId = id)}
        />
      {:else}
        <p class="empty">{t('requests.phone.none')}</p>
      {/if}

      {#if decided.length > 0}
        <section class="phone-decided" aria-labelledby={decidedId}>
          <SectionHead title={t('requests.recent')} id={decidedId} size="md" gap={10} />
          <DecidedList items={decided.slice(0, 6)} />
        </section>
      {/if}
    {/if}
  {:else if status === 'failed'}
    <LoadFailed onretry={() => void load()} />
  {:else}
    <PageSkeleton heading cards={2} />
  {/if}
</div>

<DeclineDialog
  bind:open={declineOpen}
  request={declineTarget}
  busy={declineBusy}
  error={declineError}
  onconfirm={confirmDecline}
/>

<Toast message={toaster.message} />

<style>
  .page {
    outline: none;
  }
  .head {
    padding-top: 8px;
  }
  .tabs {
    margin-bottom: 26px;
  }
  .layout {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 24px;
  }
  .results {
    flex: 999 1 620px;
    min-width: 0;
  }
  .block {
    margin-bottom: 32px;
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr));
    gap: 16px;
    align-items: stretch;
  }
  .empty {
    font-size: 14px;
    line-height: 20px;
    color: var(--muted);
  }
  .empty-action {
    margin-top: 12px;
  }
  .soft-link {
    padding: 0;
    border: 0;
    background: transparent;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .soft-link:hover {
    color: var(--text);
  }

  .side {
    flex: 1 1 340px;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .phone-head {
    padding: 10px 4px 12px;
  }
  .phone-head h1 {
    font-size: 28px;
    font-weight: 700;
    line-height: 34px;
    color: var(--text);
  }
  .phone-head p {
    margin-top: 2px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .phone-decided {
    margin-top: 22px;
  }
</style>
