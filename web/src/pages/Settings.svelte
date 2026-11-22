<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import Toast from '../lib/ui/Toast.svelte';
  import { getMetadataStatus } from '../lib/core/catalog';
  import { t } from '../lib/core/i18n';
  import { providerRows } from '../lib/core/providerRows';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { navigate, type Match } from '../lib/core/router';
  import { session } from '../lib/core/session.svelte';
  import { createToaster } from '../lib/core/toast.svelte.ts';
import type { PanelRow } from '../lib/core/views';
  import PeoplePanel from './requests/PeoplePanel.svelte';
  import AlertsPanel from './settings/AlertsPanel.svelte';
  import ClaudePanel from './settings/ClaudePanel.svelte';
  import LanguagePanel from './settings/LanguagePanel.svelte';
  import AboutPanel from './settings/AboutPanel.svelte';
  import ConnectionPanel from './settings/ConnectionPanel.svelte';
  import DownloadAppsPanel from './settings/DownloadAppsPanel.svelte';
  import FoldersPanel from './settings/FoldersPanel.svelte';
  import MediaServersPanel from './settings/MediaServersPanel.svelte';
  import ProfilePanel from './settings/ProfilePanel.svelte';
  import ProfilesPanel from './settings/ProfilesPanel.svelte';
  import SourcesPanel from './settings/SourcesPanel.svelte';
  import SubtitlesPanel from './settings/SubtitlesPanel.svelte';
  import TitleInfoPanel from './settings/TitleInfoPanel.svelte';
  import WatchlistsPanel from './settings/WatchlistsPanel.svelte';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const role = $derived(session.me?.role);
  const isOwner = $derived(role === 'owner');
  // A member has no server settings: Settings is their profile.
  const isMember = $derived(role === 'member');

  const toaster = createToaster();
  const note = (message: string): void => toaster.show(message);

  // Every tab is live: Connections, Library folders, Title info, Quality rules, People & limits,
  // Subtitles, Claude & apps, Language and About.
  const tabs = $derived<TabItem[]>([
    { id: 'connections', label: t('settings.tab.connections') },
    { id: 'folders', label: t('settings.tab.folders') },
    { id: 'title-info', label: t('settings.tab.titleInfo') },
    { id: 'quality', label: t('settings.tab.quality') },
    { id: 'people', label: t('settings.tab.people') },
    { id: 'subtitles', label: t('settings.tab.subtitles') },
    { id: 'claude', label: t('settings.tab.claude') },
    { id: 'language', label: t('settings.tab.language') },
    { id: 'about', label: t('settings.tab.about') },
  ]);
  // The address says which tab is open, so a link can land on one (Explore links to Title info).
  const tab = $derived.by(() => {
    const wanted = match.query.get('tab');
    return tabs.find((item) => item.id === wanted)?.id ?? 'connections';
  });
  const tabName = $derived(tabs.find((item) => item.id === tab)?.label ?? '');

  function choose(id: string): void {
    navigate(id === 'connections' ? '/settings' : `/settings?tab=${id}`, { replace: true });
  }

  // A link such as /settings?add=qbittorrent (from Downloads or a release search) opens that add
  // dialog once, on Connections; the address is then tidied so a reload does not open it again.
  const addParam = untrack(() => match.query.get('add') ?? '');
  const addApp = addParam === 'downloadApp' || ['qbittorrent', 'deluge', 'sabnzbd'].includes(addParam);
  const addSource = addParam === 'source';
  onMount(() => {
    if (addParam) navigate('/settings', { replace: true });
  });

  // Title info: the real status of TMDB, TVmaze and MusicBrainz. Asked again each time Connections
  // opens, so a key saved on Title info shows here at once.
  const info = resource(
    () => (tab === 'connections' && isOwner ? 'info' : 'idle'),
    (key, signal) => (key === 'info' ? getMetadataStatus(signal) : Promise.resolve(undefined)),
  );
  const infoRows = $derived<PanelRow[]>(
    info.data
      ? providerRows(info.data).map((row) => ({
          key: row.name,
          name: row.name,
          ...(row.tag ? { tag: row.tag } : {}),
          status: row.status,
          state: row.state,
          buttons:
            row.action === 'edit' && isOwner
              ? [
                  {
                    id: 'edit',
                    label: t('settings.action.edit'),
                    aria: t('settings.rowAria', { action: t('settings.action.edit'), name: row.name }),
                    onclick: () => choose('title-info'),
                  },
                ]
              : [],
        }))
      : [],
  );
</script>

{#if isMember}
  <PageHeader title={t('tabs.profile')} scale="large" gap={18} />
  <ProfilePanel />
{:else}
  <PageHeader title={t('route.settings')} scale="large" gap={18} />

  <div class="tabs">
    <Tabs
      kind="nav"
      items={tabs}
      value={tab}
      onchange={choose}
      label={t('settings.sections')}
      prefix="settings"
    />
  </div>

  {#if tab === 'folders'}
    <FoldersPanel canEdit={isOwner} />
  {:else if tab === 'title-info'}
    <TitleInfoPanel {isOwner} />
  {:else if tab === 'quality'}
    {#if isOwner}
      <ProfilesPanel onnote={note} />
    {:else}
      <Panel tag="div" padding="18px">
        <p class="soon">{t('settings.ownerOnly')}</p>
      </Panel>
    {/if}
  {:else if tab === 'people'}
    <div class="narrow"><PeoplePanel canEdit={isOwner} onsaved={() => note(t('limits.saved'))} /></div>
  {:else if tab === 'subtitles'}
    <SubtitlesPanel {isOwner} onnote={note} />
  {:else if tab === 'claude'}
    <ClaudePanel onnote={note} />
  {:else if tab === 'language'}
    <div class="language"><LanguagePanel /></div>
  {:else if tab === 'about'}
    <AboutPanel {isOwner} onnote={note} />
  {:else if tab !== 'connections'}
    <Panel tag="div" padding="18px">
      <p class="soon"><span class="sr-only">{tabName}: </span>{t('settings.soon')}</p>
    </Panel>
  {:else if isOwner}
    <div class="grid">
      <DownloadAppsPanel addKind={addApp ? addParam : undefined} onnote={note} />
      <MediaServersPanel onnote={note} />
      <SourcesPanel openAdd={addSource} onnote={note} />
      {#if info.data}
        <ConnectionPanel
          title={t('settings.panel.info')}
          rows={infoRows}
          note={t('settings.note.info')}
        />
      {:else if info.failed}
        <LoadFailed onretry={info.reload} />
      {/if}
      <WatchlistsPanel onnote={note} />
      <AlertsPanel onnote={note} />
    </div>
  {:else}
    <!-- An admin may look around and scan folders, but the connections hold other services' passwords. -->
    <Panel tag="div" padding="18px">
      <p class="soon">{t('settings.ownerOnlyConnections')}</p>
    </Panel>
  {/if}
{/if}

<Toast message={toaster.message} />

<style>
  .tabs {
    margin-bottom: 22px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .narrow {
    max-width: 560px;
  }
  .language {
    max-width: 880px;
  }
  .soon {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
</style>
