<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Button from '../lib/ui/Button.svelte';
  import CheckField from '../lib/ui/CheckField.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import DataTable, { type DataColumn } from '../lib/ui/DataTable.svelte';
  import FormSheet from '../lib/ui/FormSheet.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import type { IconName } from '../lib/ui/icons';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import ProgressBar from '../lib/ui/ProgressBar.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import StatusDot from '../lib/ui/StatusDot.svelte';
  import Thumb from '../lib/ui/Thumb.svelte';
  import Toast from '../lib/ui/Toast.svelte';
  import { failureText, titlePath } from '../lib/core/catalog';
  import {
    getDownloadsSummary,
    listDownloads,
    pauseDownload,
    removeDownload,
    resumeDownload,
    IMPORT_STEPS,
    type DownloadEvent,
    type DownloadsSummary,
    type DownloadView,
  } from '../lib/core/downloadsApi';
  import { debounce, onEvent, onResync } from '../lib/core/events';
  import { dotOf, stateText, statsOf } from '../lib/core/downloadWords';
  import { formatBytes, formatWhen, qualityLabel } from '../lib/core/format';
  import { formatList, t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { session } from '../lib/core/session.svelte';
  import { listMediaServers } from '../lib/core/serversApi';
  import { createToaster } from '../lib/core/toast.svelte.ts';
  import type { ImportStepState } from '../lib/core/views';

  const toaster = createToaster();
  const canSee = $derived(session.me?.role === 'owner' || session.me?.role === 'admin');

  // ---- loading ----
  let active = $state.raw<DownloadView[]>([]);
  let history = $state.raw<DownloadView[]>([]);
  let summary = $state.raw<DownloadsSummary | undefined>();
  let serverName = $state('');
  let loaded = $state(false);
  let failed = $state(false);

  async function load(): Promise<void> {
    try {
      const [now, past, sum] = await Promise.all([
        listDownloads({ state: 'active', limit: 200 }),
        listDownloads({ state: 'history', limit: 40 }),
        getDownloadsSummary(),
      ]);
      active = now.downloads;
      history = past.downloads;
      summary = sum;
      loaded = true;
      failed = false;
    } catch {
      if (!loaded) failed = true;
    }
  }

  /** The media server the importer tells to rescan: the first enabled one, when the owner can see them. */
  async function loadServerName(): Promise<void> {
    try {
      const servers = await listMediaServers();
      serverName = servers.find((s) => s.enabled)?.name ?? '';
    } catch {
      serverName = ''; // not built yet, or not allowed: "your media server" it is
    }
  }

  function onDownloadEvent(raw: unknown): void {
    const ev = raw as DownloadEvent;
    if (ev.type === 'state' && ev.id !== undefined && ev.importStep !== undefined) {
      // The import moved to its next step: show it at once; the reload below fills in the rest.
      active = active.map((d) =>
        d.id === ev.id ? { ...d, state: ev.state ?? d.state, importStep: ev.importStep } : d,
      );
    }
    if (ev.type === 'progress' && ev.items) {
      // Progress is batched: move the bars at once, no reload.
      const byId = new Map(ev.items.map((i) => [i.id, i]));
      active = active.map((d) => {
        const p = byId.get(d.id);
        return p
          ? { ...d, state: p.state, progress: p.progress, speedBps: p.speedBps, etaSeconds: p.etaSeconds }
          : d;
      });
      return;
    }
    soon.call();
  }

  const soon = debounce(() => void load(), 250);

  onMount(() => {
    if (!canSee) return;
    void load();
    void loadServerName();
    const stop = onEvent('downloads', onDownloadEvent);
    const stopResync = onResync(() => soon.call());
    return () => {
      soon.cancel();
      stop();
      stopResync();
    };
  });

  // ---- what to show ----
  const order: Record<string, number> = {
    importing: 0,
    completed: 1,
    downloading: 2,
    stalled: 3,
    paused: 4,
    queued: 5,
  };
  const now = $derived([...active].sort((a, b) => (order[a.state] ?? 9) - (order[b.state] ?? 9)));

  function nameOf(d: DownloadView): string {
    if (!d.title) return d.releaseTitle;
    return d.title.year ? `${d.title.name} (${d.title.year})` : d.title.name;
  }

  const counts = $derived({
    downloading: summary?.downloading ?? 0,
    importing: summary?.importing ?? 0,
    queued: summary?.queued ?? 0,
  });
  const connected = $derived((summary?.clients ?? []).filter((c) => c.ok).map((c) => c.name));
  const subtitle = $derived(
    summary
      ? connected.length > 0
        ? t('downloads.summary', {
            ...counts,
            apps: formatList(connected),
          })
        : t('downloads.summaryNoApps', counts)
      : undefined,
  );

  // ---- pause, resume, remove ----
  let working = $state<number | null>(null);

  function replace(next: DownloadView): void {
    active = active.map((d) => (d.id === next.id ? next : d));
  }

  async function pause(d: DownloadView): Promise<void> {
    if (working !== null) return;
    working = d.id;
    try {
      replace(await pauseDownload(d.id));
    } catch (err) {
      toaster.show(failureText(err));
    } finally {
      working = null;
    }
  }

  async function resume(d: DownloadView): Promise<void> {
    if (working !== null) return;
    working = d.id;
    try {
      replace(await resumeDownload(d.id));
    } catch (err) {
      toaster.show(failureText(err));
    } finally {
      working = null;
    }
  }

  let removing = $state<DownloadView | undefined>();
  let removeOpen = $state(false);
  let removeBusy = $state(false);
  let removeError = $state('');
  let deleteData = $state(false);
  let root: HTMLElement | undefined = $state();

  function askRemove(d: DownloadView): void {
    removing = d;
    deleteData = false;
    removeError = '';
    removeOpen = true;
  }

  async function confirmRemove(): Promise<void> {
    const d = removing;
    if (!d || removeBusy) return;
    removeBusy = true;
    removeError = '';
    try {
      await removeDownload(d.id, deleteData);
      active = active.filter((x) => x.id !== d.id);
      removeOpen = false;
      toaster.show(t('downloads.removed', { title: nameOf(d) }));
      void load();
      // The button that opened the dialog is gone with its row: keep keyboard focus on the page.
      await tick();
      root?.focus({ preventScroll: true });
    } catch (err) {
      removeError = failureText(err);
    } finally {
      removeBusy = false;
    }
  }

  // ---- the import strip, driven by the state of the download that is being imported ----
  const importing = $derived(
    now.find((d) => d.state === 'importing') ?? now.find((d) => d.state === 'completed'),
  );

  interface Step {
    label: string;
    state: ImportStepState;
    detail: string;
  }

  /** The last part of a path, whichever way it is written. */
  function lastPart(path: string): string {
    return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
  }

  /**
   * The strip follows the step the importer reports (`importStep`: checking, renaming, moving,
   * rescanning, done). Before it reports one (the download just finished and waits for its turn) the
   * check is the next thing to happen. Steps before the current one are done, the current one is
   * working, the rest wait.
   */
  const steps = $derived.by<Step[]>(() => {
    const d = importing;
    if (!d) return [];
    const server = serverName || t('downloads.step.yourServer');
    const at = d.importStep ? IMPORT_STEPS.indexOf(d.importStep) : -1; // -1: not started
    const stateOf = (index: number): ImportStepState => (at > index ? 'done' : at === index ? 'working' : 'pending');
    // "Checked" is the first step: it is working while the importer has not said anything yet.
    const checkState: ImportStepState = at < 0 ? 'working' : stateOf(0);
    const target = d.importTarget ?? '';
    return [
      {
        label: t('downloads.step.downloaded'),
        state: 'done',
        detail: t('downloads.step.downloadedDetail', {
          size: d.size > 0 ? formatBytes(d.size) : t('downloads.step.allOfIt'),
          client: d.client.name,
        }),
      },
      {
        label: t('downloads.step.checked'),
        state: checkState,
        detail:
          checkState === 'done'
            ? t('downloads.step.checkedDone')
            : d.state === 'importing'
              ? t('downloads.step.checkedBusy')
              : t('downloads.step.checkedWaiting'),
      },
      {
        label: t('downloads.step.renamed'),
        state: stateOf(1),
        detail:
          stateOf(1) === 'pending'
            ? t('downloads.step.afterCheck')
            : target
              ? t(stateOf(1) === 'done' ? 'downloads.step.renamedDone' : 'downloads.step.renamedBusy', {
                  name: lastPart(target),
                })
              : t(stateOf(1) === 'done' ? 'downloads.step.renamedDonePlain' : 'downloads.step.renamedBusyPlain'),
      },
      {
        label: t('downloads.step.moved'),
        state: stateOf(2),
        detail:
          stateOf(2) === 'pending'
            ? t('downloads.step.afterRename')
            : target
              ? t(stateOf(2) === 'done' ? 'downloads.step.movedDone' : 'downloads.step.movedBusy', { where: target })
              : t(stateOf(2) === 'done' ? 'downloads.step.movedDonePlain' : 'downloads.step.movedBusyPlain'),
      },
      {
        label: t('downloads.step.rescanned', { server }),
        state: stateOf(3),
        detail:
          stateOf(3) === 'pending'
            ? t('downloads.step.afterMove')
            : t(stateOf(3) === 'done' ? 'downloads.step.rescannedDone' : 'downloads.step.rescannedBusy', { server }),
      },
    ];
  });

  // ---- history ----
  const outcomeIcon: Record<string, IconName> = {
    imported: 'check',
    upgraded: 'upgrade',
    failed: 'warning',
    removed: 'trash',
  };

  /** How a finished download ended: the server says (`outcome`); an older one is read from the state. */
  function outcomeOf(d: DownloadView): string {
    if (d.outcome) return d.outcome;
    if (d.state === 'failed' || d.state === 'removed') return d.state;
    return 'imported';
  }

  function outcomeText(d: DownloadView): string {
    switch (outcomeOf(d)) {
      case 'failed':
        return d.error ? t('downloads.outcome.failedWhy', { why: d.error }) : t('downloads.outcome.failed');
      case 'removed':
        return t('downloads.outcome.removed');
      case 'upgraded':
        return t('downloads.outcome.upgraded');
      default:
        return t('downloads.outcome.imported');
    }
  }

  const columns: DataColumn[] = [
    { id: 'when', label: t('downloads.col.when') },
    { id: 'title', label: t('downloads.col.title') },
    { id: 'what', label: t('downloads.col.what') },
    { id: 'source', label: t('downloads.col.source') },
    { id: 'size', label: t('downloads.col.size'), align: 'end' },
  ];
  const template = '136px minmax(170px,1.2fr) minmax(220px,1.6fr) 100px 72px';

  // ---- the download apps panel ----
  const KINDS: Array<{ kind: string; name: string }> = [
    { kind: 'qbittorrent', name: 'qBittorrent' },
    { kind: 'deluge', name: 'Deluge' },
    { kind: 'sabnzbd', name: 'SABnzbd' },
  ];
  const activePerClient = $derived.by(() => {
    const per = new Map<number, number>();
    for (const d of active) per.set(d.client.id, (per.get(d.client.id) ?? 0) + 1);
    return per;
  });
  const notSetUp = $derived(
    KINDS.filter((k) => !(summary?.clients ?? []).some((c) => c.kind === k.kind)),
  );

  const uid = $props.id();
  const nowId = `dl-now-${uid}`;
  const stepsId = `dl-steps-${uid}`;
  const appsId = `dl-apps-${uid}`;
  const historyId = `dl-history-${uid}`;
</script>

<div class="page" tabindex="-1" bind:this={root}>
  {#if !canSee}
    <PageHeader title={t('route.downloads')} scale="large" />
    <p class="quiet">{t('downloads.ownerOnly')}</p>
  {:else if loaded}
    <PageHeader title={t('route.downloads')} subtitle={subtitle} scale="large" />

    <div class="cols">
      <div class="main">
        <section class="now" aria-labelledby={nowId}>
          <SectionHead title={t('downloads.now')} id={nowId} />
          {#if now.length === 0}
            <Panel tag="div" radius="md" padding="16px">
              <p class="quiet">{t('downloads.nothingNow')}</p>
            </Panel>
          {:else}
            <ul class="list">
              {#each now as item (item.id)}
                {@const name = nameOf(item)}
                <li>
                  <Panel tag="div" radius="md" padding="14px 16px">
                    <div class="item">
                      <Thumb
                        title={item.title?.name ?? item.releaseTitle}
                        width={54}
                        height={68}
                        fontSize={30}
                        radius={6}
                        imageRef={item.title?.posterRef || undefined}
                      />
                      <div class="body">
                        <div class="title-row">
                          {#if item.title}
                            <a class="title" href={titlePath(item.title.kind, item.title.id)}>{name}</a>
                          {:else}
                            <span class="title">{name}</span>
                          {/if}
                          <Chip tone="solid" size="tag">{qualityLabel(item.quality)}</Chip>
                        </div>
                        <div class="bar">
                          <ProgressBar
                            value={item.progress * 100}
                            label={t('downloads.progress', { title: name })}
                            tone={item.state === 'completed' || item.state === 'importing' ? 'accent' : 'text'}
                            rounded
                          />
                        </div>
                        <div class="state-row">
                          <span class="step">
                            <StatusDot kind={dotOf(item.state)}>{stateText(item.state)}</StatusDot>
                          </span>
                          <span class="stats">{statsOf(item)}</span>
                        </div>
                      </div>
                      <div class="buttons">
                        {#if item.state === 'paused'}
                          <Button
                            size="sm"
                            disabled={working === item.id}
                            aria-label={t('downloads.resumeAria', { title: name })}
                            onclick={() => resume(item)}
                          >
                            <Icon name="play" size={16} stroke={2} />
                            {t('action.resume')}
                          </Button>
                        {:else if item.state === 'downloading' || item.state === 'queued' || item.state === 'stalled'}
                          <Button
                            size="sm"
                            disabled={working === item.id}
                            aria-label={t('downloads.pauseAria', { title: name })}
                            onclick={() => pause(item)}
                          >
                            <Icon name="pause" size={16} stroke={2} />
                            {t('action.pause')}
                          </Button>
                        {/if}
                        <Button
                          size="sm"
                          aria-label={t('downloads.removeAria', { title: name })}
                          onclick={() => askRemove(item)}
                        >
                          <Icon name="trash" size={16} stroke={2} />
                          {t('action.remove')}
                        </Button>
                      </div>
                    </div>
                  </Panel>
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        <Panel labelledby={stepsId} padding="18px">
          <h2 id={stepsId} class="h2">{t('downloads.steps.heading')}</h2>
          {#if importing}
            <p class="steps-sub">
              {t('downloads.steps.sub', {
                title: nameOf(importing),
                quality: qualityLabel(importing.quality),
                server: serverName || t('downloads.step.yourServer'),
              })}
            </p>
            <ol class="steps">
              {#each steps as step (step.label)}
                <li class="step-box {step.state}">
                  <div class="step-bar" aria-hidden="true"></div>
                  <div class="step-head">
                    {#if step.state === 'done'}
                      <Icon name="check" size={18} stroke={2.4} />
                    {:else if step.state === 'working'}
                      <svg
                        viewBox="0 0 24 24"
                        width="18"
                        height="18"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2.4"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        aria-hidden="true"
                        focusable="false"
                      >
                        <path d="M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z" opacity="0.28" />
                        <path d="M12 3a9 9 0 0 1 9 9" />
                      </svg>
                    {:else}
                      <svg
                        viewBox="0 0 24 24"
                        width="18"
                        height="18"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        stroke-dasharray="2.5 3.2"
                        aria-hidden="true"
                        focusable="false"
                      >
                        <path d="M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16z" />
                      </svg>
                    {/if}
                    <span class="step-label">{step.label}</span>
                  </div>
                  <div class="step-state">{t(`downloads.step.${step.state}`)}</div>
                  <div class="step-detail">{step.detail}</div>
                </li>
              {/each}
            </ol>
          {:else}
            <p class="steps-sub">{t('downloads.steps.none')}</p>
          {/if}
        </Panel>
      </div>

      <div class="side">
        <Panel labelledby={appsId} padding="18px">
          <SectionHead title={t('downloads.apps.heading')} id={appsId} size="md" gap={4}>
            <a class="soft-link" href="/settings">{t('downloads.apps.manage')}</a>
          </SectionHead>
          <ul class="apps">
            {#each summary?.clients ?? [] as app (app.id)}
              {@const n = activePerClient.get(app.id) ?? 0}
              <li>
                <div class="app-text">
                  <div class="app-name">{app.name}</div>
                  <div class="app-status">
                    <StatusDot kind={app.ok ? 'solid' : 'hollow'}>
                      {#if app.ok}
                        {n > 0 ? t('downloads.apps.connectedActive', { n }) : t('downloads.apps.connected')}
                      {:else}
                        {app.lastError
                          ? t('downloads.apps.notAnswering', { why: app.lastError })
                          : t('downloads.apps.notAnsweringPlain')}
                      {/if}
                    </StatusDot>
                  </div>
                </div>
              </li>
            {/each}
            {#each notSetUp as app (app.kind)}
              <li>
                <div class="app-text">
                  <div class="app-name">{app.name}</div>
                  <div class="app-status">
                    <StatusDot kind="hollow">{t('downloads.apps.notSetUp')}</StatusDot>
                  </div>
                </div>
                {#if session.me?.role === 'owner'}
                  <Button
                    size="compact"
                    href="/settings?add={app.kind}"
                    aria-label={t('downloads.apps.addAria', { name: app.name })}
                  >
                    <Icon name="plus" size={16} stroke={2} />
                    {t('action.add')}
                  </Button>
                {/if}
              </li>
            {/each}
          </ul>
        </Panel>
      </div>
    </div>

    <section aria-labelledby={historyId}>
      <SectionHead title={t('downloads.history.heading')} id={historyId} />
      <Panel tag="div" padding="0" clip>
        {#if history.length === 0}
          <p class="quiet pad">{t('downloads.history.empty')}</p>
        {:else if !$isDesktop}
          <!-- A phone reads the history as a list: a five-column table would have to scroll sideways. -->
          <ul class="h-list" aria-label={t('downloads.history.label')}>
            {#each history as row (row.id)}
              <li>
                <div class="h-list-top">
                  <span class="h-title">{nameOf(row)} · {qualityLabel(row.quality)}</span>
                  <span class="h-when">{formatWhen(row.completedAt ?? row.updatedAt)}</span>
                </div>
                <span class="h-what {row.state}">
                  <Icon name={outcomeIcon[outcomeOf(row)] ?? 'check'} size={18} stroke={2} />
                  <span class="h-text">{outcomeText(row)}</span>
                </span>
                <span class="h-soft">
                  {row.client.name}{row.size > 0 ? ` · ${formatBytes(row.size)}` : ''}
                </span>
              </li>
            {/each}
          </ul>
        {:else}
          <DataTable
            label={t('downloads.history.label')}
            {columns}
            {template}
            minWidth={780}
            variant="inset"
            padX={16}
            rowMin={56}
            softRule
            rows={history}
            rowKey={(row) => row.id}
          >
            {#snippet cell(row: DownloadView, column: DataColumn)}
              {#if column.id === 'when'}
                <span class="h-when">{formatWhen(row.completedAt ?? row.updatedAt)}</span>
              {:else if column.id === 'title'}
                <span class="h-title">{nameOf(row)} · {qualityLabel(row.quality)}</span>
              {:else if column.id === 'what'}
                <span class="h-what {row.state}">
                  <Icon name={outcomeIcon[outcomeOf(row)] ?? 'check'} size={18} stroke={2} />
                  <span class="h-text">{outcomeText(row)}</span>
                </span>
              {:else if column.id === 'source'}
                <span class="h-soft">{row.client.name}</span>
              {:else}
                <span class="h-soft num">{row.size > 0 ? formatBytes(row.size) : ''}</span>
              {/if}
            {/snippet}
          </DataTable>
        {/if}
      </Panel>
    </section>
  {:else if failed}
    <LoadFailed onretry={() => void load()} />
  {:else}
    <PageSkeleton heading cards={2} />
  {/if}
</div>

<FormSheet
  bind:open={removeOpen}
  title={t('downloads.remove.title', { title: removing ? nameOf(removing) : '' })}
  intro={removing ? t('downloads.remove.body', { client: removing.client.name }) : undefined}
  submitLabel={t('downloads.remove.confirm')}
  busy={removeBusy}
  error={removeError}
  onsubmit={confirmRemove}
>
  <CheckField
    label={t('downloads.remove.deleteData')}
    help={t('downloads.remove.deleteDataHelp')}
    bind:checked={deleteData}
  />
</FormSheet>

<Toast message={toaster.message} />

<style>
  .page {
    outline: none;
  }
  .quiet {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .pad {
    padding: 16px;
  }
  .cols {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 24px;
    margin-bottom: 28px;
  }
  .main {
    flex: 999 1 560px;
    min-width: 0;
  }
  .side {
    flex: 1 1 300px;
    min-width: 0;
  }
  .now {
    margin-bottom: 22px;
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .item {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 14px 16px;
  }
  .body {
    flex: 1 1 220px;
    min-width: 0;
  }
  .title-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 10px;
  }
  .title {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    text-decoration: none;
    overflow-wrap: anywhere;
  }
  a.title:hover {
    text-decoration: underline;
  }
  .bar {
    margin: 10px 0 9px;
  }
  .state-row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: 4px 16px;
  }
  .step {
    color: var(--text-2);
  }
  .stats {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.01em;
    color: var(--text-3);
  }
  .buttons {
    flex: 0 0 auto;
    display: flex;
    gap: 8px;
  }

  .h2 {
    font-size: 16px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
  }
  .steps-sub {
    margin: 2px 0 16px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .steps {
    display: flex;
    flex-wrap: wrap;
    gap: 16px 12px;
  }
  .step-box {
    flex: 1 1 104px;
    min-width: 0;
  }
  .step-bar {
    height: 3px;
    border-radius: 2px;
    background: var(--track-faint);
  }
  .step-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 10px;
    color: var(--muted);
  }
  .step-label {
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-3);
  }
  .step-state {
    margin-top: 2px;
    font-size: 12.5px;
    font-weight: 500;
    line-height: 17px;
    color: var(--text-3);
  }
  .step-detail {
    margin-top: 4px;
    font-size: 12.5px;
    line-height: 17px;
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  .done .step-bar {
    background: var(--text);
  }
  .done .step-head {
    color: var(--text);
  }
  .done .step-label {
    color: var(--text);
  }
  .done .step-state {
    color: var(--text-2);
  }
  .working .step-bar {
    background: var(--accent);
  }
  .working .step-head {
    color: var(--accent);
  }
  .working .step-label {
    color: var(--text);
  }
  .working .step-state {
    color: var(--accent);
    font-weight: 600;
  }

  .soft-link {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
    text-decoration: none;
  }
  .soft-link:hover {
    color: var(--text);
  }
  .apps li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 10px 12px;
    padding: 14px 0;
  }
  .apps li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  .app-text {
    flex: 1 1 170px;
    min-width: 0;
  }
  .app-name {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .app-status {
    margin-top: 3px;
    color: var(--text-3);
  }

  .h-when {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    font-variant-numeric: tabular-nums;
    color: var(--text-3);
  }
  .h-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .h-what {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 13.5px;
    font-weight: 500;
    line-height: 19px;
    color: var(--text-2);
  }
  .h-what :global(svg) {
    margin-top: 1px;
    color: var(--text);
  }
  .h-what.failed :global(svg) {
    color: var(--accent);
  }
  .h-what.removed :global(svg) {
    color: var(--muted);
  }
  .h-text {
    overflow-wrap: anywhere;
  }
  .h-list li {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 12px 16px;
  }
  .h-list li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  .h-list-top {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: 2px 12px;
  }
  .h-soft {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
  }
  .num {
    font-variant-numeric: tabular-nums;
  }
</style>
