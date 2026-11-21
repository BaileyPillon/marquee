<script lang="ts">
  import { onMount } from 'svelte';
  import AlsoInLibrary from '../lib/ui/AlsoInLibrary.svelte';
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import BackLink from '../lib/ui/BackLink.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import HistoryPanel from '../lib/ui/HistoryPanel.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import type { IconName } from '../lib/ui/icons';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import PreviewTag from '../lib/ui/PreviewTag.svelte';
  import ReleasesPanel from '../lib/ui/ReleasesPanel.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import TitleNotFound from '../lib/ui/TitleNotFound.svelte';
  import TitleToolbar from '../lib/ui/TitleToolbar.svelte';
  import { ApiError } from '../lib/core/api';
  import { getTitle, titlePath, type Person } from '../lib/core/catalog';
  import { formatBytes, formatDay, formatRuntime } from '../lib/core/format';
  import { t } from '../lib/core/i18n';
  import { libraryIndex } from '../lib/core/libraryIndex.svelte.ts';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { navigate, type Match } from '../lib/core/router';
  import { session } from '../lib/core/session.svelte';
  import { heroTheme } from '../lib/core/themes';
  import { titleFileInfo } from '../lib/core/titleFiles';
  import CopyPanel from './title/CopyPanel.svelte';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const page = resource(() => match.params['id'] ?? '', getTitle);
  const d = $derived(page.data);
  const notFound = $derived(page.error instanceof ApiError && page.error.status === 404);

  onMount(() => libraryIndex.ensure());

  // A show or an artist opened through /movies/<id>: go to its own page.
  $effect(() => {
    if (d && d.kind !== 'movie') navigate(titlePath(d.kind, d.id), { replace: true });
  });

  const role = $derived(session.me?.role);
  const canManage = $derived(role === 'owner' || role === 'admin');

  // What is on disk comes with the title; the owner's file list adds the path of the biggest file.
  const info = $derived(d ? titleFileInfo(d) : undefined);
  const files = $derived(d ? libraryIndex.titles.get(d.id) : undefined);
  const file = $derived(files ? [...files.files].sort((a, b) => b.size - a.size)[0] : undefined);

  const status = $derived.by<{ text: string; icon: IconName }>(() => {
    if (!d) return { text: '', icon: 'check' };
    if (info?.inLibrary) {
      return {
        text: info.quality ? t('hero.inLibrary', { quality: info.quality }) : t('title.status.inLibrary'),
        icon: 'check',
      };
    }
    if (info) {
      return {
        text: d.monitored ? t('title.status.waiting') : t('title.status.notMonitored'),
        icon: 'time',
      };
    }
    return { text: t('title.status.added', { date: formatDay(d.addedAt) }), icon: 'check' };
  });

  const directors = $derived(
    (d?.people ?? []).filter((p) => p.role === 'director').map((p) => p.name),
  );
  const meta = $derived(
    d
      ? [
          d.year ? String(d.year) : '',
          formatRuntime(d.runtimeMin),
          d.certification,
          directors.length ? t('title.directedBy', { names: directors.join(', ') }) : '',
        ]
          .filter(Boolean)
          .join(' · ')
      : '',
  );
  const blurb = $derived(d ? d.overview || d.tagline || t('title.noOverview') : '');

  const cast = $derived((d?.people ?? []).filter((p) => p.role === 'actor').slice(0, 14));
  const crew = $derived((d?.people ?? []).filter((p) => p.role !== 'actor').slice(0, 12));

  let askOpen = $state(false);

  const uid = $props.id();
  const libraryId = `title-library-${uid}`;
  const castId = `title-cast-${uid}`;

  // "Search for a better copy" opens the releases panel and asks the sources for an HD copy.
  let releases: ReleasesPanel | undefined = $state();
  let releasesBox: HTMLElement | undefined = $state();

  async function searchBetter(): Promise<void> {
    releasesBox?.scrollIntoView({ block: 'start' });
    await releases?.run('hd');
  }

  function fileName(relPath: string): string {
    return relPath.split('/').pop() ?? relPath;
  }

  function crewRole(p: Person): string {
    return p.job || p.role;
  }
</script>

{#if d && d.kind === 'movie'}
  <BackLink href="/movies">{t('nav.movies')}</BackLink>

  <Hero
    size="title"
    label={d.name}
    title={d.name}
    year={d.year}
    status={status.text}
    statusIcon={status.icon}
    backdropRef={d.backdropRef || undefined}
    {meta}
    {blurb}
    theme={heroTheme(d.name, d.kind)}
    padY={24}
    blurbWidth={500}
    gap={16}
  >
    {#snippet chips()}
      {#each d.genres as genre (genre)}
        <Chip>{genre}</Chip>
      {/each}
    {/snippet}
  </Hero>

  <TitleToolbar
    detail={d}
    label={t('title.actionsFor', { title: d.name })}
    monitorLabel={t('title.monitor')}
    askLabel={t('hero.askFor', { quality: t('ask.quality.uhd.short') })}
    onask={() => (askOpen = true)}
    onupdated={page.set}
  />

  <div class="duo">
    <Panel labelledby={libraryId}>
      <SectionHead title={t('title.library.heading')} id={libraryId} align="center" gap={14}>
        {#if info?.inLibrary}
          <Badge tone="solid" size="md">
            <Icon name="check" size={12} stroke={2.6} />
            {info.quality || t('status.library')}
          </Badge>
        {/if}
      </SectionHead>
      {#if file}
        <div class="file">
          <span class="file-icon"><Icon name="file" size={24} /></span>
          <div class="file-text">
            <div class="file-name">{fileName(file.relPath)}</div>
            <div class="file-meta">
              {formatBytes(file.size)} · {file.relPath} · {t('title.library.added', { date: formatDay(file.addedAt) })}
            </div>
          </div>
        </div>
      {:else if info?.inLibrary}
        <div class="file">
          <span class="file-icon"><Icon name="file" size={24} /></span>
          <div class="file-text">
            <div class="file-name">{info.quality || t('title.status.inLibrary')}</div>
            <div class="file-meta">
              {[
                info.sizeBytes > 0 ? formatBytes(info.sizeBytes) : '',
                info.count > 1 ? t('title.library.files', { n: info.count }) : '',
              ]
                .filter(Boolean)
                .join(' · ')}
            </div>
          </div>
        </div>
      {:else if info}
        <p class="empty">{t('title.library.none')}</p>
      {:else}
        <p class="empty">{t('title.library.unknown')}</p>
      {/if}
      {#if canManage}
        <div class="row">
          <Button variant="quiet" size="compact" onclick={searchBetter}>{t('title.library.better')}</Button>
          <PreviewTag />
          <Button variant="quiet" size="compact" disabled>{t('title.library.rename')}</Button>
          <Button variant="quiet" size="compact" disabled>{t('title.library.delete')}</Button>
        </div>
      {/if}
    </Panel>

    <CopyPanel titleId={d.id} kind="movie" onask={() => (askOpen = true)} />
  </div>

  {#if canManage}
    <div class="block" bind:this={releasesBox}>
      <ReleasesPanel bind:this={releases} titleId={d.id} name={d.name} kind="movie" quality="hd" />
    </div>
  {/if}

  <div class="duo spaced">
    <Panel labelledby={castId} padding="18px 20px 20px">
      <h2 id={castId} class="plain-head">{t('title.castCrew')}</h2>
      {#if cast.length === 0 && crew.length === 0}
        <p class="empty">{t('title.noPeople')}</p>
      {/if}
      {#if cast.length > 0}
        <h3 class="caps first">{t('title.cast')}</h3>
        <ul class="chips">
          {#each cast as person (person.name + person.job)}
            <li><Chip tone="quiet" size="xl">{person.name}</Chip></li>
          {/each}
        </ul>
      {/if}
      {#if crew.length > 0}
        <h3 class="caps" class:first={cast.length === 0}>{t('title.crew')}</h3>
        <ul class="chips">
          {#each crew as person (person.name + person.role + person.job)}
            <li>
              <Chip tone="quiet" size="xl">
                <span>{person.name}</span>
                <span class="role">{crewRole(person)}</span>
              </Chip>
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>

    <HistoryPanel titleId={d.id} />
  </div>

  <AlsoInLibrary kind="movie" exceptId={d.id} heading={t('title.moreInLibrary')} />

  <AskSheet
    bind:open={askOpen}
    title={d.name}
    target={{ titleId: d.id }}
    kind="movie"
    defaultQuality="uhd"
    haveHd={!!info?.inLibrary}
  />
{:else if notFound}
  <TitleNotFound backHref="/movies" backLabel={t('nav.movies')} />
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton hero cards={2} />
{/if}

<style>
  .duo {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr));
    gap: 14px;
    margin-bottom: 14px;
  }
  .spaced {
    margin-top: 14px;
  }
  .block {
    margin-bottom: 14px;
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
  .file-meta:empty {
    display: none;
  }
  .empty {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    margin-top: 14px;
  }

  .plain-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
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
    margin-left: 6px;
    color: var(--muted);
  }
</style>
