<script lang="ts">
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import TopBar from '../lib/ui/TopBar.svelte';
  import { t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { navigate } from '../lib/core/router';
  import { getSettings } from '../lib/mock/settings';
  import ConnectionPanel from './settings/ConnectionPanel.svelte';
  import KeysPanel from './settings/KeysPanel.svelte';

  const page = resource(() => 'settings', () => getSettings());
  const view = $derived(page.data);

  // Only Connections has content so far. The other tabs say so.
  const tabs = $derived<TabItem[]>([
    { id: 'connections', label: t('settings.tab.connections') },
    { id: 'folders', label: t('settings.tab.folders') },
    { id: 'quality', label: t('settings.tab.quality') },
    { id: 'people', label: t('settings.tab.people') },
    { id: 'subtitles', label: t('settings.tab.subtitles') },
    { id: 'claude', label: t('settings.tab.claude') },
    { id: 'language', label: t('settings.tab.language') },
    { id: 'about', label: t('settings.tab.about') },
  ]);
  let tab = $state('connections');
  const tabName = $derived(tabs.find((item) => item.id === tab)?.label ?? '');
</script>

<!-- The router does not show the search bar here (it is off for Settings), so the page brings its own. -->
{#if $isDesktop}
  <TopBar onask={() => navigate('/explore')} />
{/if}

<PageHeader title={t('route.settings')} scale="large" gap={18} />

<div class="tabs">
  <Tabs
    kind="nav"
    items={tabs}
    value={tab}
    onchange={(id) => (tab = id)}
    label={t('settings.sections')}
    prefix="settings"
  />
</div>

{#if tab !== 'connections'}
  <Panel tag="div" padding="18px">
    <p class="soon"><span class="sr-only">{tabName}: </span>{t('settings.soon')}</p>
  </Panel>
{:else if view}
  <div class="grid">
    <ConnectionPanel title={t('settings.panel.apps')} rows={view.downloadApps} />
    <ConnectionPanel title={t('settings.panel.servers')} rows={view.mediaServers} />
    <ConnectionPanel
      title={t('settings.panel.sources')}
      rows={view.sources}
      note={t('settings.note.sources')}
    >
      {#snippet action()}
        <Button size="sm">
          <Icon name="plus" size={16} stroke={2.2} />
          {t('settings.addSource')}
        </Button>
      {/snippet}
    </ConnectionPanel>
    <ConnectionPanel
      title={t('settings.panel.info')}
      rows={view.titleInfo}
      note={t('settings.note.info')}
    />
    <KeysPanel keys={view.keys} />
    <ConnectionPanel title={t('settings.panel.alerts')} rows={view.alerts} />
  </div>
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton cards={4} />
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
