<script lang="ts">
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import { getMetadataStatus } from '../lib/core/catalog';
  import { t } from '../lib/core/i18n';
  import { providerRows } from '../lib/core/providerRows';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { navigate, type Match } from '../lib/core/router';
  import { session } from '../lib/core/session.svelte';
  import type { ConnectionRow } from '../lib/core/views';
  import { getSettingsPreview } from '../lib/mock/settings';
  import ConnectionPanel from './settings/ConnectionPanel.svelte';
  import FoldersPanel from './settings/FoldersPanel.svelte';
  import KeysPanel from './settings/KeysPanel.svelte';
  import ProfilePanel from './settings/ProfilePanel.svelte';
  import TitleInfoPanel from './settings/TitleInfoPanel.svelte';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const role = $derived(session.me?.role);
  const isOwner = $derived(role === 'owner');
  // A member has no server settings: Settings is their profile.
  const isMember = $derived(role === 'member');

  // Folders and Title info are live. The rest of Connections shows sample panels tagged "Preview",
  // and the other tabs say they are coming.
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

  // Connections: sample panels for what is not built, the real Title info status.
  const preview = resource(() => 'preview', () => getSettingsPreview());
  // Asked again each time Connections opens, so a key saved on Title info shows here at once.
  const info = resource(
    () => (tab === 'connections' && !isMember ? 'info' : 'idle'),
    (key, signal) => (key === 'info' ? getMetadataStatus(signal) : Promise.resolve(undefined)),
  );
  const view = $derived(preview.data);
  const infoRows = $derived<ConnectionRow[]>(info.data ? providerRows(info.data) : []);

  function editInfoRow(row: ConnectionRow): void {
    if (row.name === 'TMDB') choose('title-info');
  }
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
  {:else if tab !== 'connections'}
    <Panel tag="div" padding="18px">
      <p class="soon"><span class="sr-only">{tabName}: </span>{t('settings.soon')}</p>
    </Panel>
  {:else if view}
    <div class="grid">
      <ConnectionPanel title={t('settings.panel.apps')} rows={view.downloadApps} preview />
      <ConnectionPanel title={t('settings.panel.servers')} rows={view.mediaServers} preview />
      <ConnectionPanel
        title={t('settings.panel.sources')}
        rows={view.sources}
        note={t('settings.note.sources')}
        preview
      >
        {#snippet action()}
          <Button size="sm" disabled>
            <Icon name="plus" size={16} stroke={2.2} />
            {t('settings.addSource')}
          </Button>
        {/snippet}
      </ConnectionPanel>
      {#if info.data}
        <ConnectionPanel
          title={t('settings.panel.info')}
          rows={infoRows}
          note={t('settings.note.info')}
          onrow={isOwner ? editInfoRow : undefined}
        />
      {:else if info.failed}
        <LoadFailed onretry={info.reload} />
      {/if}
      <KeysPanel keys={view.keys} />
      <ConnectionPanel title={t('settings.panel.alerts')} rows={view.alerts} preview />
    </div>
  {:else if preview.failed}
    <LoadFailed onretry={preview.reload} />
  {:else}
    <PageSkeleton cards={4} />
  {/if}
{/if}

<style>
  .tabs {
    margin-bottom: 22px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    gap: 16px;
  }
  .soon {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
</style>
