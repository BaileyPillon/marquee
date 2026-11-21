<script lang="ts">
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import BackLink from '../lib/ui/BackLink.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import DataTable, { type DataColumn } from '../lib/ui/DataTable.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import type { IconName } from '../lib/ui/icons';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import PosterRow from '../lib/ui/PosterRow.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import { t } from '../lib/core/i18n';
  import { resource } from '../lib/core/resource.svelte.ts';
  import type { Match } from '../lib/core/router';
  import { statusLabel, statusTone } from '../lib/core/status';
  import type { HistoryKind, Release } from '../lib/core/views';
  import { getTitle } from '../lib/mock/title';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const page = resource(() => match.params['id'] ?? '', getTitle);
  const view = $derived(page.data);

  let askOpen = $state(false);

  const historyIcon: Record<HistoryKind, IconName> = {
    found: 'search',
    asked: 'requests',
    imported: 'check',
    grabbed: 'downloads',
  };

  const columns: DataColumn[] = [
    { id: 'release', label: t('title.col.release') },
    { id: 'size', label: t('title.col.size') },
    { id: 'source', label: t('title.col.source') },
    { id: 'age', label: t('title.col.age') },
    { id: 'score', label: t('title.col.score') },
    { id: 'action', label: t('title.col.action'), hideLabel: true },
  ];
  const template = 'minmax(260px,2fr) 76px 112px 68px minmax(300px,2fr) 84px';

  const uid = $props.id();
  const libraryId = `title-library-${uid}`;
  const copyId = `title-4k-${uid}`;
  const releasesId = `title-releases-${uid}`;
  const castId = `title-cast-${uid}`;
  const historyId = `title-history-${uid}`;

  function profile(name: string): string {
    return `/explore?q=${encodeURIComponent(name)}`;
  }
</script>

{#if view}
  <BackLink href="/movies">{t('nav.movies')}</BackLink>

  <Hero
    size="title"
    label={view.title}
    title={view.title}
    year={view.year}
    status={view.status}
    meta={view.meta}
    blurb={view.blurb}
    theme={view.theme}
    padY={24}
    blurbWidth={500}
    gap={16}
  >
    {#snippet chips()}
      {#each view.genres as genre (genre)}
        <Chip href="/explore?genre={encodeURIComponent(genre)}">{genre}</Chip>
      {/each}
    {/snippet}
  </Hero>

  <div class="actions" role="group" aria-label={t('title.actionsFor', { title: view.title })}>
    <Button variant="primary">
      <Icon name="play" size={18} filled />
      {t('hero.openIn', { server: view.server })}
    </Button>
    <Button onclick={() => (askOpen = true)}>{t('hero.askFor', { quality: view.askQuality })}</Button>
    <Button>
      <Icon name="trailer" size={18} stroke={2} />
      {t('title.trailer')}
    </Button>
    <Button icon aria-label={t('title.moreActions')}>
      <Icon name="more" size={20} stroke={3} />
    </Button>
  </div>

  <div class="duo">
    <Panel labelledby={libraryId}>
      <SectionHead title={t('title.library.heading')} id={libraryId} align="center" gap={14}>
        <Badge tone="solid" size="md">
          <Icon name="check" size={12} stroke={2.6} />
          {view.library.quality}
        </Badge>
      </SectionHead>
      <div class="file">
        <span class="file-icon"><Icon name="file" size={24} /></span>
        <div class="file-text">
          <div class="file-name">{view.library.fileName}</div>
          <div class="file-meta">{view.library.details}</div>
        </div>
      </div>
      <div class="row">
        <Button variant="quiet" size="compact">{t('title.library.better')}</Button>
        <Button variant="quiet" size="compact">{t('title.library.rename')}</Button>
        <Button variant="quiet" size="compact">{t('title.library.delete')}</Button>
      </div>
    </Panel>

    <Panel labelledby={copyId}>
      <SectionHead title={t('title.copy4k.heading')} id={copyId} align="center" gap={14}>
        <Badge tone={statusTone(view.copy4k.status)} size="md">{statusLabel(view.copy4k.status)}</Badge>
      </SectionHead>
      <p class="headline">{view.copy4k.headline}</p>
      <p class="asked">{view.copy4k.asked}</p>
      <div class="decide">
        <Button variant="primary">
          <Icon name="check" size={18} stroke={2.4} />
          {t('action.approve')}
        </Button>
        <Button>
          <Icon name="close" size={18} stroke={2.4} />
          {t('action.decline')}
        </Button>
      </div>
      <div class="rule">
        <span class="rule-icon"><Icon name="rules" size={18} /></span>
        <span>{view.copy4k.rule}</span>
      </div>
    </Panel>
  </div>

  <Panel labelledby={releasesId} padding="18px 20px 8px">
    <SectionHead title={t('title.releases.heading')} id={releasesId} gap={12}>
      <span class="note">{view.releases.note}</span>
    </SectionHead>
    <DataTable
      label={t('title.releases.label')}
      {columns}
      {template}
      minWidth={980}
      rows={view.releases.items}
      rowKey={(row) => row.name}
      padBottom={6}
    >
      {#snippet cell(row: Release, column: DataColumn)}
        {#if column.id === 'release'}
          <span class="release">{row.name}</span>
        {:else if column.id === 'size'}
          <span class="num strong">{row.size}</span>
        {:else if column.id === 'source'}
          <span class="soft">{row.source}</span>
        {:else if column.id === 'age'}
          <span class="soft num">{row.age}</span>
        {:else if column.id === 'score'}
          <div class="score-cell">
            <span class="score" class:best={row.best}>{row.score}</span>
            <span class="reason">{row.reason}</span>
          </div>
        {:else}
          <div class="grab">
            <Button
              variant={row.best ? 'primary' : 'outline'}
              size="sm"
              aria-label={t('title.grabAria', { name: row.name })}
            >
              <Icon name="downloads" size={16} stroke={2.2} />
              {t('title.grab')}
            </Button>
          </div>
        {/if}
      {/snippet}
    </DataTable>
  </Panel>

  <div class="duo spaced">
    <Panel labelledby={castId} padding="18px 20px 20px">
      <h2 id={castId} class="plain-head">{t('title.castCrew')}</h2>
      <h3 class="caps first">{t('title.cast')}</h3>
      <div class="chips">
        {#each view.cast as name (name)}
          <Chip tone="quiet" size="xl" href={profile(name)}>{name}</Chip>
        {/each}
      </div>
      <h3 class="caps">{t('title.crew')}</h3>
      <div class="chips">
        {#each view.crew as member (member.name)}
          <Chip tone="quiet" size="xl" href={profile(member.name)}>
            <span>{member.name}</span>
            <span class="role">{member.role}</span>
          </Chip>
        {/each}
      </div>
    </Panel>

    <Panel labelledby={historyId} padding="18px 20px 12px">
      <h2 id={historyId} class="plain-head history-head">{t('title.history')}</h2>
      <ol class="history">
        {#each view.history as event, i (i)}
          <li>
            <span class="date">{event.date}</span>
            <span class="event-icon"><Icon name={historyIcon[event.kind]} size={16} stroke={1.9} /></span>
            <span class="event-text">{event.text}</span>
          </li>
        {/each}
      </ol>
    </Panel>
  </div>

  <PosterRow title={t('title.moreLike')} href="/movies" items={view.similar} />

  <AskSheet bind:open={askOpen} title={view.title} titleId={view.id} />
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton hero cards={2} />
{/if}

<style>
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    margin-bottom: 18px;
  }
  .duo {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr));
    gap: 14px;
    margin-bottom: 14px;
  }
  .spaced {
    margin-top: 14px;
  }

  .file {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    padding: 14px;
    border-radius: var(--radius-md);
    background: var(--rail);
    box-shadow: inset 0 0 0 1px var(--hairline);
  }
  .file-icon {
    display: flex;
    margin-top: 1px;
    color: var(--text-3);
  }
  .file-text {
    flex: 1 1 auto;
    min-width: 0;
  }
  .file-name {
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .file-meta {
    margin-top: 4px;
    font-size: 13px;
    line-height: 19px;
    color: var(--text-3);
    overflow-wrap: anywhere;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
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

  .note {
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .release {
    font-size: 13.5px;
    font-weight: 600;
    line-height: 19px;
    font-variant-numeric: tabular-nums;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .num {
    font-variant-numeric: tabular-nums;
  }
  .strong {
    font-size: 13.5px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
  }
  .soft {
    font-size: 13.5px;
    font-weight: 500;
    line-height: 19px;
    color: var(--text-2);
  }
  .score-cell {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    min-width: 0;
  }
  .score {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    min-width: 36px;
    height: 26px;
    padding: 0 8px;
    border: 1px solid var(--line);
    border-radius: 5px;
    font-size: 13px;
    font-weight: 700;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    color: var(--text);
  }
  .score.best {
    background: var(--text);
    border-color: var(--text);
    color: var(--ground);
  }
  .reason {
    min-width: 0;
    padding-top: 4px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-2);
  }
  .grab {
    display: flex;
    justify-content: flex-end;
  }

  .plain-head {
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .history-head {
    margin-bottom: 12px;
  }
  .caps {
    margin: 16px 0 8px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .caps.first {
    margin-top: 14px;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .role {
    color: var(--muted);
  }

  .history {
    border-bottom: 1px solid var(--hairline-strong);
  }
  .history li {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 10px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .date {
    flex: 0 0 52px;
    font-size: 13px;
    font-weight: 600;
    line-height: 20px;
    font-variant-numeric: tabular-nums;
    color: var(--text);
  }
  .event-icon {
    display: flex;
    margin-top: 2px;
    color: var(--muted);
  }
  .event-text {
    flex: 1 1 auto;
    min-width: 0;
    font-size: 13.5px;
    line-height: 20px;
    color: var(--text-2);
  }
</style>
