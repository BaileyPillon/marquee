<script lang="ts">
  import { tick } from 'svelte';
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LabelValue from '../lib/ui/LabelValue.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import Toast from '../lib/ui/Toast.svelte';
  import { t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { createToaster } from '../lib/core/toast.svelte.ts';
  import type { DecidedRequest, RequestDecision, WaitingRequest } from '../lib/core/views';
  import { decideRequest, getRequests } from '../lib/mock/requests';
  import DecidedList from './requests/DecidedList.svelte';
  import PhoneApproval from './requests/PhoneApproval.svelte';
  import RequestCard from './requests/RequestCard.svelte';

  const page = resource(() => 'requests', () => getRequests());
  const view = $derived(page.data);
  const toaster = createToaster();

  // The lists start from what the server sent and change as the owner decides. Late answers from
  // the server will replace them the same way.
  let waiting = $state.raw<WaitingRequest[]>([]);
  let decided = $state.raw<DecidedRequest[]>([]);
  let decidedNow = $state(0);
  $effect(() => {
    if (!view) return;
    waiting = view.waiting;
    decided = view.decided;
    decidedNow = 0;
  });

  let tab = $state('waiting');
  let busy = $state<string | null>(null);
  let activeId = $state<string | undefined>();
  const active = $derived(waiting.find((r) => r.id === activeId) ?? waiting[0]);

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
      ? decided.filter((d) => d.outcome === 'approved')
      : tab === 'declined'
        ? decided.filter((d) => d.outcome === 'declined')
        : tab === 'watchlists'
          ? decided.filter((d) => d.fromWatchlist)
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
    t('requests.summary', {
      waiting: waiting.length,
      decided: (view?.decidedThisWeek ?? 0) + decidedNow,
    }),
  );

  let root: HTMLElement | undefined = $state();

  async function decide(request: WaitingRequest, outcome: RequestDecision): Promise<void> {
    if (busy) return;
    busy = request.id;
    try {
      await decideRequest(request.id, outcome);
    } catch {
      toaster.show(t('requests.toast.failed'));
      busy = null;
      return;
    }
    waiting = waiting.filter((r) => r.id !== request.id);
    decided = [
      {
        id: request.id,
        title: request.title,
        detail: t(
          outcome === 'approved' ? 'requests.decidedBy.approved' : 'requests.decidedBy.declined',
          { who: request.requester },
        ),
        outcome,
        when: t('requests.today'),
        fromWatchlist: false,
      },
      ...decided,
    ];
    decidedNow += 1;
    busy = null;
    toaster.show(
      t(outcome === 'approved' ? 'requests.toast.approved' : 'requests.toast.declined', {
        title: request.title,
        who: request.requester,
      }),
    );
    // The button that was pressed is gone with its card: keep keyboard focus on the page.
    await tick();
    root?.focus({ preventScroll: true });
  }

  const uid = $props.id();
  const waitingId = `req-waiting-${uid}`;
  const decidedId = `req-decided-${uid}`;
  const peopleId = `req-people-${uid}`;
  const watchId = `req-watch-${uid}`;
</script>

<div class="page" tabindex="-1" bind:this={root}>
  {#if view}
    {#if $isDesktop}
      <div class="head">
        <PageHeader title={t('route.requests')} subtitle={summary} preview />
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
                      onapprove={() => decide(request, 'approved')}
                      ondecline={() => decide(request, 'declined')}
                    />
                  {/each}
                </div>
              {:else}
                <p class="empty">{t('requests.empty')}</p>
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
              <DecidedList items={shownDecided} />
            {:else}
              <p class="empty">{t('requests.empty')}</p>
            {/if}
          </section>
        </div>

        <div class="side">
          <Panel labelledby={peopleId} padding="20px">
            <h2 id={peopleId} class="h2">{t('requests.people')}</h2>
            <p class="note">{t('requests.peopleNote')}</p>
            <ul class="people">
              {#each view.people as person (person.name)}
                <li>
                  <span class="avatar" aria-hidden="true">{person.name.charAt(0)}</span>
                  <div class="person">
                    <div class="name">{person.name}</div>
                    <LabelValue
                      variant="plain"
                      items={[
                        { label: t('requests.hdAsks'), value: person.hd },
                        { label: t('requests.fourKAsks'), value: person.fourK },
                        {
                          label: t('requests.autoApprove'),
                          value: person.autoApprove ? t('toggle.on') : t('toggle.off'),
                          tone: person.autoApprove ? 'on' : 'off',
                        },
                      ]}
                    />
                  </div>
                </li>
              {/each}
            </ul>
            <div class="people-foot">
              <Button>
                <Icon name="sliders" size={18} stroke={1.9} />
                {t('requests.editLimits')}
              </Button>
            </div>
          </Panel>

          <Panel labelledby={watchId} padding="20px">
            <h2 id={watchId} class="h2">{t('requests.watchlists')}</h2>
            <p class="watch-head">{view.watchlists.headline}</p>
            <p class="watch-detail">{view.watchlists.detail}</p>
            <div class="watch-foot">
              <Button>
                <Icon name="refresh" size={18} stroke={1.9} />
                {t('requests.checkNow')}
              </Button>
            </div>
          </Panel>
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
          onapprove={() => decide(active, 'approved')}
          ondecline={() => decide(active, 'declined')}
          onpick={(id) => (activeId = id)}
        />
      {:else}
        <p class="empty">{t('requests.phone.none')}</p>
      {/if}
    {/if}
  {:else if page.failed}
    <LoadFailed onretry={page.reload} />
  {:else}
    <PageSkeleton heading cards={2} />
  {/if}
</div>

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
  .h2 {
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .note {
    margin: 2px 0 8px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .people li {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .avatar {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: var(--avatar);
    font-size: 14px;
    font-weight: 600;
    line-height: 1;
    color: var(--text);
  }
  .person {
    flex: 1 1 0;
    min-width: 0;
  }
  .name {
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .people-foot {
    padding-top: 16px;
    border-top: 1px solid var(--hairline-strong);
  }
  .watch-head {
    margin-top: 8px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .watch-detail {
    margin-top: 4px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .watch-foot {
    margin-top: 14px;
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
</style>
