<script lang="ts">
  import { onMount } from 'svelte';
  import Field from '../../lib/accounts/Field.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LabelValue from '../../lib/ui/LabelValue.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import StatusDot from '../../lib/ui/StatusDot.svelte';
  import { health } from '../../lib/core/api';
  import { failureText } from '../../lib/core/catalog';
  import { formatAgo, formatBytes } from '../../lib/core/format';
  import { t, type MessageKey } from '../../lib/core/i18n';
  import {
    getServerSettings,
    getSystemStatus,
    saveServerSettings,
    type ConnectionStatus,
    type SystemStatus,
  } from '../../lib/core/systemApi';

  interface Props {
    /** The owner changes how Marquee is reached; an admin only looks. */
    isOwner: boolean;
    onnote?: (message: string) => void;
  }

  let { isOwner, onnote }: Props = $props();

  /** Where the code is and under which licence. Names, not words: they are not translated. */
  const REPOSITORY = 'https://github.com/BaileyPillon/marquee';
  const LICENSE = 'MIT';

  const uid = $props.id();
  const aboutId = `about-${uid}`;
  const statusId = `about-status-${uid}`;
  const connectionsId = `about-connections-${uid}`;
  const jobsId = `about-jobs-${uid}`;
  const serverId = `about-server-${uid}`;

  // ---- the version (public) and the status (owner and admins) ----
  type Load = 'loading' | 'ready' | 'failed';
  let status = $state<Load>('loading');
  let sys = $state.raw<SystemStatus | undefined>();
  let version = $state('');
  let refreshing = $state(false);

  async function load(): Promise<void> {
    refreshing = true;
    try {
      sys = await getSystemStatus();
      version = sys.version;
      status = 'ready';
    } catch {
      // The version is public, so About still says it when the status itself cannot be read.
      try {
        version = (await health()).version ?? version;
      } catch {
        // Nothing answers: the failure note says so.
      }
      if (status !== 'ready') status = 'failed';
    } finally {
      refreshing = false;
    }
  }

  onMount(() => {
    void load();
    if (isOwner) void loadServer();
  });

  // ---- words for the numbers ----
  function uptime(seconds: number): string {
    const minutes = Math.floor(seconds / 60);
    if (minutes < 1) return t('about.uptime.lessThanMinute');
    const days = Math.floor(minutes / 1440);
    const hours = Math.floor((minutes % 1440) / 60);
    const mins = minutes % 60;
    return [
      days > 0 ? t('about.uptime.days', { n: days }) : '',
      hours > 0 ? t('about.uptime.hours', { n: hours }) : '',
      days === 0 && mins > 0 ? t('about.uptime.minutes', { n: mins }) : '',
    ]
      .filter(Boolean)
      .join(' ');
  }

  function kindLabel(kind: string): string {
    const keys: Record<string, MessageKey> = {
      downloadClient: 'about.kind.downloadClient',
      source: 'about.kind.source',
      mediaServer: 'about.kind.mediaServer',
      metadata: 'about.kind.metadata',
      alertChannel: 'about.kind.alertChannel',
      plexWatchlist: 'about.kind.plexWatchlist',
    };
    const key = keys[kind];
    return key ? t(key) : kind;
  }

  function connectionLine(c: ConnectionStatus): string {
    if (!c.ok) return c.lastError ? t('about.connection.problem', { why: c.lastError }) : t('about.connection.problemPlain');
    return c.lastOkAt ? t('about.connection.okAgo', { ago: formatAgo(c.lastOkAt) }) : t('about.connection.ok');
  }

  const problems = $derived((sys?.connections ?? []).filter((c) => !c.ok).length);
  const failedJobs = $derived((sys?.jobs ?? []).filter((j) => j.lastError).length);

  const facts = $derived.by(() => {
    const s = sys;
    if (!s) return [];
    const rows = [
      { label: t('about.fact.uptime'), value: uptime(s.uptimeSeconds) },
      { label: t('about.fact.runtime'), value: `${s.go} · ${s.os}/${s.arch}` },
      { label: t('about.fact.config'), value: s.configDir || t('about.fact.unknown') },
    ];
    if (s.disk) {
      rows.push({
        label: t('about.fact.disk'),
        value: t('about.fact.diskValue', { free: formatBytes(s.disk.freeBytes), total: formatBytes(s.disk.totalBytes) }),
      });
    }
    for (const d of s.databases) {
      rows.push({
        label: t('about.fact.database', { name: d.name }),
        value: d.ok ? formatBytes(d.sizeBytes) : t('about.fact.databaseDown', { size: formatBytes(d.sizeBytes) }),
      });
    }
    return rows;
  });

  // ---- how Marquee is reached: the public address and the reverse proxies it trusts ----
  let serverStatus = $state<Load>('loading');
  let publicUrl = $state('');
  let proxies = $state('');
  let saving = $state(false);
  let saveError = $state('');

  async function loadServer(): Promise<void> {
    try {
      const s = await getServerSettings();
      publicUrl = s.publicUrl;
      proxies = s.trustedProxies.join(', ');
      serverStatus = 'ready';
    } catch {
      if (serverStatus !== 'ready') serverStatus = 'failed';
    }
  }

  async function saveServer(): Promise<void> {
    if (saving) return;
    saving = true;
    saveError = '';
    try {
      const saved = await saveServerSettings({
        publicUrl: publicUrl.trim(),
        trustedProxies: proxies.split(/[\s,;]+/).filter(Boolean),
      });
      publicUrl = saved.publicUrl;
      proxies = saved.trustedProxies.join(', ');
      onnote?.(t('about.server.saved'));
      void load(); // the status shows what is now in force
    } catch (err) {
      saveError = failureText(err);
    } finally {
      saving = false;
    }
  }
</script>

<div class="grid">
  <Panel labelledby={aboutId} padding="18px">
    <SectionHead title={t('about.heading')} id={aboutId} size="md" align="center" gap={4} minHeight={36}>
      <Button size="sm" disabled={refreshing} onclick={() => void load()}>
        <Icon name="refresh" size={16} stroke={2.2} />
        {refreshing ? t('about.checking') : t('about.refresh')}
      </Button>
    </SectionHead>
    <p class="help">{t('about.intro')}</p>
    <LabelValue
      items={[
        { label: t('about.version'), value: version || t('about.fact.unknown') },
        { label: t('about.license'), value: LICENSE },
      ]}
    />
    <div class="links">
      <Button size="sm" variant="outline" external href={REPOSITORY}>
        <Icon name="globe" size={16} stroke={2} />
        {t('about.repository')}
      </Button>
    </div>
  </Panel>

  {#if status === 'loading'}
    <Panel tag="div" padding="18px">
      <div class="loading" role="status" aria-busy="true">
        <span class="sr-only">{t('load.loading')}</span>
        <Skeleton height="120px" radius="var(--radius-md)" />
      </div>
    </Panel>
  {:else if status === 'failed' || !sys}
    <LoadFailed onretry={() => void load()} />
  {:else}
    <Panel labelledby={statusId} padding="18px">
      <SectionHead title={t('about.status.heading')} id={statusId} size="md" align="center" gap={4} minHeight={36}>
        {#if problems > 0 || failedJobs > 0}
          <Chip tone="inverse" size="micro">{t('about.status.attention')}</Chip>
        {:else}
          <Chip tone="solid" size="micro">{t('about.status.allWell')}</Chip>
        {/if}
      </SectionHead>
      <LabelValue items={facts} />
    </Panel>

    <Panel labelledby={connectionsId} padding="18px">
      <SectionHead title={t('about.connections.heading')} id={connectionsId} size="md" align="center" gap={4} minHeight={36} />
      {#if sys.connections.length === 0}
        <p class="help">{t('about.connections.none')}</p>
      {:else}
        <ul class="rows">
          {#each sys.connections as c (c.kind + ':' + (c.id ?? c.name))}
            <li>
              <div class="line">
                <span class="name">{c.name}</span>
                <Chip tone="faint" size="micro">{kindLabel(c.kind)}</Chip>
              </div>
              <StatusDot kind={c.ok ? 'solid' : 'hollow'}>{connectionLine(c)}</StatusDot>
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>

    <Panel labelledby={jobsId} padding="18px">
      <SectionHead title={t('about.jobs.heading')} id={jobsId} size="md" align="center" gap={4} minHeight={36} />
      {#if sys.jobs.length === 0}
        <p class="help">{t('about.jobs.none')}</p>
      {:else}
        <ul class="rows">
          {#each sys.jobs as job (job.name)}
            <li>
              <div class="line">
                <code class="name mono">{job.name}</code>
                {#if job.running}<Chip tone="faint" size="micro">{t('about.jobs.running')}</Chip>{/if}
              </div>
              <p class="job-line">
                {job.lastRun ? t('about.jobs.lastRun', { ago: formatAgo(job.lastRun) }) : t('about.jobs.neverRan')}
                · {t('about.jobs.counts', { runs: job.runs, failed: job.failures })}
              </p>
              {#if job.lastError}<p class="problem">{t('about.jobs.lastError', { why: job.lastError })}</p>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>
  {/if}

  {#if isOwner}
    <Panel labelledby={serverId} padding="18px">
      <SectionHead title={t('about.server.heading')} id={serverId} size="md" align="center" gap={4} minHeight={36} />
      {#if serverStatus === 'loading'}
        <div class="loading" role="status" aria-busy="true">
          <span class="sr-only">{t('load.loading')}</span>
          <Skeleton height="64px" radius="var(--radius-md)" />
        </div>
      {:else if serverStatus === 'failed'}
        <LoadFailed onretry={() => void loadServer()} />
      {:else}
        <form
          class="form"
          novalidate
          onsubmit={(e) => {
            e.preventDefault();
            void saveServer();
          }}
        >
          <Field
            label={t('about.server.publicUrl')}
            type="url"
            name="publicUrl"
            autocomplete="off"
            placeholder="https://marquee.example.com"
            help={t('about.server.publicUrlHelp')}
            bind:value={publicUrl}
          />
          <Field
            label={t('about.server.proxies')}
            name="trustedProxies"
            autocomplete="off"
            placeholder="10.0.0.0/8, 192.168.1.5"
            help={t('about.server.proxiesHelp')}
            bind:value={proxies}
          />
          {#if (sys?.server.trustedFromEnvironment ?? []).length > 0}
            <p class="help">
              {t('about.server.fromEnvironment', { list: (sys?.server.trustedFromEnvironment ?? []).join(', ') })}
            </p>
          {/if}
          {#if saveError}<p class="problem" role="alert">{saveError}</p>{/if}
          <div class="actions">
            <Button variant="primary" type="submit" disabled={saving}>
              {saving ? t('confirm.working') : t('about.server.save')}
            </Button>
          </div>
        </form>
      {/if}
    </Panel>
  {/if}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .loading {
    margin-top: 8px;
  }
  .help {
    margin: 8px 0 0;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .links {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
  }
  .rows {
    margin-top: 6px;
  }
  .rows li {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 12px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
  }
  .name {
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: 13.5px;
  }
  .job-line {
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .problem {
    font-size: 13.5px;
    line-height: 19px;
    color: var(--danger);
    overflow-wrap: anywhere;
  }
  .form {
    display: flex;
    flex-direction: column;
    gap: 14px;
    margin-top: 10px;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
