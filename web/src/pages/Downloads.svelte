<script lang="ts">
  import Button from '../lib/ui/Button.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import DataTable, { type DataColumn } from '../lib/ui/DataTable.svelte';
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
  import { t } from '../lib/core/i18n';
  import { resource } from '../lib/core/resource.svelte.ts';
  import type { DownloadKind, HistoryOutcome, HistoryRow } from '../lib/core/views';
  import { getDownloads } from '../lib/mock/downloads';

  const page = resource(() => 'downloads', () => getDownloads());
  const view = $derived(page.data);

  const dotKind: Record<DownloadKind, 'solid' | 'accent' | 'hollow'> = {
    downloading: 'solid',
    importing: 'accent',
    queued: 'hollow',
  };
  const outcomeIcon: Record<HistoryOutcome, IconName> = {
    imported: 'check',
    failed: 'warning',
    upgraded: 'upgrade',
    removed: 'trash',
  };

  const columns: DataColumn[] = [
    { id: 'when', label: t('downloads.col.when') },
    { id: 'title', label: t('downloads.col.title') },
    { id: 'what', label: t('downloads.col.what') },
    { id: 'source', label: t('downloads.col.source') },
    { id: 'size', label: t('downloads.col.size'), align: 'end' },
  ];
  const template = '116px minmax(170px,1.2fr) minmax(220px,1.6fr) 100px 72px';

  const uid = $props.id();
  const nowId = `dl-now-${uid}`;
  const stepsId = `dl-steps-${uid}`;
  const appsId = `dl-apps-${uid}`;
  const historyId = `dl-history-${uid}`;

  const summary = $derived(
    view
      ? t('downloads.summary', {
          downloading: view.counts.downloading,
          importing: view.counts.importing,
          queued: view.counts.queued,
          apps: view.connectedApps,
        })
      : undefined,
  );
</script>

{#if view}
  <PageHeader title={t('route.downloads')} subtitle={summary} scale="large" />

  <div class="cols">
    <div class="main">
      <section class="now" aria-labelledby={nowId}>
        <SectionHead title={t('downloads.now')} id={nowId} />
        <ul class="list">
          {#each view.now as item (item.id)}
            <li>
              <Panel tag="div" radius="md" padding="14px 16px">
                <div class="item">
                  <Thumb title={item.title} width={54} height={68} fontSize={30} radius={6} />
                  <div class="body">
                    <div class="title-row">
                      <span class="title">{item.title}</span>
                      <Chip tone="solid" size="tag">{item.quality}</Chip>
                    </div>
                    <div class="bar">
                      <ProgressBar
                        value={item.pct}
                        label={t('downloads.progress', { title: item.title })}
                        tone={item.kind === 'importing' ? 'accent' : 'text'}
                        rounded
                      />
                    </div>
                    <div class="state-row">
                      <span class="step"><StatusDot kind={dotKind[item.kind]}>{item.step}</StatusDot></span>
                      <span class="stats">{item.stats}</span>
                    </div>
                  </div>
                  <div class="buttons">
                    {#if item.canPause}
                      <Button size="sm" aria-label={t('downloads.pauseAria', { title: item.title })}>
                        <Icon name="pause" size={16} stroke={2} />
                        {t('action.pause')}
                      </Button>
                    {/if}
                    <Button size="sm" aria-label={t('downloads.removeAria', { title: item.title })}>
                      <Icon name="trash" size={16} stroke={2} />
                      {t('action.remove')}
                    </Button>
                  </div>
                </div>
              </Panel>
            </li>
          {/each}
        </ul>
      </section>

      <Panel labelledby={stepsId} padding="18px">
        <h2 id={stepsId} class="h2">{t('downloads.steps.heading')}</h2>
        <p class="steps-sub">
          {t('downloads.steps.sub', {
            title: view.importing.title,
            quality: view.importing.quality,
            server: view.importing.server,
          })}
        </p>
        <ol class="steps">
          {#each view.importing.steps as step (step.label)}
            <li class="step-box {step.state}" class:wide={step.mono}>
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
              <div class="step-detail" class:mono={step.mono}>{step.detail}</div>
            </li>
          {/each}
        </ol>
      </Panel>
    </div>

    <div class="side">
      <Panel labelledby={appsId} padding="18px">
        <SectionHead title={t('downloads.apps.heading')} id={appsId} size="md" gap={4}>
          <a class="soft-link" href="/settings">{t('downloads.apps.manage')}</a>
        </SectionHead>
        <ul class="apps">
          {#each view.apps as app (app.name)}
            <li>
              <div class="app-text">
                <div class="app-name">{app.name}</div>
                <div class="app-status">
                  <StatusDot kind={app.connected ? 'solid' : 'hollow'}>{app.status}</StatusDot>
                </div>
              </div>
              {#if !app.connected}
                <Button size="compact" aria-label={t('downloads.apps.addAria', { name: app.name })}>
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
      <DataTable
        label={t('downloads.history.label')}
        {columns}
        {template}
        minWidth={780}
        variant="inset"
        padX={16}
        rowMin={56}
        softRule
        rows={view.history}
        rowKey={(row) => row.id}
      >
        {#snippet cell(row: HistoryRow, column: DataColumn)}
          {#if column.id === 'when'}
            <span class="h-when">{row.when}</span>
          {:else if column.id === 'title'}
            <span class="h-title">{row.title}</span>
          {:else if column.id === 'what'}
            <span class="h-what {row.outcome}">
              <Icon name={outcomeIcon[row.outcome]} size={18} stroke={2} />
              <span class="h-text">{row.what}</span>
            </span>
          {:else if column.id === 'source'}
            <span class="h-soft">{row.source}</span>
          {:else}
            <span class="h-soft num">{row.size}</span>
          {/if}
        {/snippet}
      </DataTable>
    </Panel>
  </section>
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton heading cards={2} />
{/if}

<style>
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
  .step-box.wide {
    flex: 2.6 1 230px;
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
  .step-detail.mono {
    font-family: var(--font-mono);
    font-size: 12px;
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
