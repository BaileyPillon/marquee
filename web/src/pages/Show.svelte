<script lang="ts">
  import { onMount } from 'svelte';
  import AlsoInLibrary from '../lib/ui/AlsoInLibrary.svelte';
  import ArtPanel from '../lib/ui/ArtPanel.svelte';
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import BackLink from '../lib/ui/BackLink.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import type { IconName } from '../lib/ui/icons';
  import LabelValue from '../lib/ui/LabelValue.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import PreviewTag from '../lib/ui/PreviewTag.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import TitleNotFound from '../lib/ui/TitleNotFound.svelte';
  import TitleToolbar from '../lib/ui/TitleToolbar.svelte';
  import { horizonArt } from '../lib/ui/art';
  import { ApiError } from '../lib/core/api';
  import { getTitle, titlePath, type Episode } from '../lib/core/catalog';
  import { formatAirDate, formatBytes, formatDay, isFuture, seasonCount, sentence } from '../lib/core/format';
  import { t } from '../lib/core/i18n';
  import { libraryIndex } from '../lib/core/libraryIndex.svelte.ts';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { navigate, type Match } from '../lib/core/router';
  import { heroTheme } from '../lib/core/themes';
  import type { EpisodeState } from '../lib/core/views';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const page = resource(() => match.params['id'] ?? '', getTitle);
  const d = $derived(page.data);
  const notFound = $derived(page.error instanceof ApiError && page.error.status === 404);

  onMount(() => libraryIndex.ensure());

  $effect(() => {
    if (d && d.kind !== 'show') navigate(titlePath(d.kind, d.id), { replace: true });
  });

  const known = $derived(libraryIndex.status === 'ready');
  const artwork = $derived(d ? horizonArt(d.year) : undefined);

  // ---- seasons and episodes ----
  const seasons = $derived(
    d ? [...d.seasons].sort((a, b) => (a.number || 99999) - (b.number || 99999)) : [],
  );
  const episodesOf = (season: number): Episode[] =>
    d ? d.episodes.filter((e) => e.season === season).sort((a, b) => a.number - b.number) : [];

  const hasFile = (e: Episode): boolean => libraryIndex.episodes.has(e.id);

  // The season to open on: the latest one with a file in the library, else the first real season.
  let picked = $state<number | undefined>();
  const season = $derived.by(() => {
    if (picked !== undefined && seasons.some((s) => s.number === picked)) return picked;
    if (known) {
      const withFile = seasons.filter((s) => episodesOf(s.number).some(hasFile));
      const last = withFile[withFile.length - 1];
      if (last) return last.number;
    }
    return (seasons.find((s) => s.number > 0) ?? seasons[0])?.number;
  });
  const episodes = $derived(season === undefined ? [] : episodesOf(season));

  const seasonTabs = $derived<TabItem[]>(
    seasons.map((s) => ({
      id: String(s.number),
      label: s.number === 0 ? t('show.specials') : t('show.season', { n: s.number }),
    })),
  );

  interface Row {
    ep: Episode;
    state: EpisodeState;
    text: string;
  }

  function rowOf(ep: Episode): Row {
    const file = libraryIndex.episodes.get(ep.id);
    if (file) {
      const text = [t('show.state.library'), file.quality, file.size > 0 ? formatBytes(file.size) : '']
        .filter(Boolean)
        .join(' · ');
      return { ep, state: 'library', text };
    }
    const air = formatAirDate(ep.airDate);
    if (!ep.airDate || isFuture(ep.airDate)) {
      return {
        ep,
        state: 'upcoming',
        text: air ? t('show.state.upcoming', { date: air }) : t('show.state.upcomingNoDate'),
      };
    }
    if (known) {
      return { ep, state: 'missing', text: ep.monitored ? t('show.state.wanted') : t('show.state.missing') };
    }
    return {
      ep,
      state: 'unknown',
      text: libraryIndex.status === 'loading' ? t('show.state.checking') : t('show.state.unknown'),
    };
  }

  const rows = $derived(episodes.map(rowOf));
  const have = $derived(known ? episodes.filter(hasFile).length : 0);

  const stateIcon: Record<EpisodeState, IconName> = {
    library: 'check',
    downloading: 'downloads',
    missing: 'search',
    upcoming: 'time',
    unknown: 'alert',
  };

  // ---- the header ----
  const regular = $derived(d ? d.episodes.filter((e) => e.season > 0) : []);
  const haveAll = $derived(known ? regular.filter(hasFile).length : 0);
  const status = $derived.by<{ text: string; icon: IconName }>(() => {
    if (!d) return { text: '', icon: 'check' };
    if (known) {
      return haveAll > 0
        ? { text: t('show.status.library', { have: haveAll, total: regular.length }), icon: 'check' }
        : { text: t('show.status.none'), icon: 'time' };
    }
    return { text: t('title.status.added', { date: formatDay(d.addedAt) }), icon: 'check' };
  });
  const meta = $derived(
    d
      ? [
          d.year ? String(d.year) : '',
          d.genres.slice(0, 2).join(', '),
          seasons.filter((s) => s.number > 0).length
            ? seasonCount(seasons.filter((s) => s.number > 0).length)
            : '',
          sentence(d.status),
        ]
          .filter(Boolean)
          .join(' · ')
      : '',
  );

  let askOpen = $state(false);

  const uid = $props.id();
  const seasonId = `show-season-${uid}`;
  const rulesId = `show-rules-${uid}`;
</script>

{#if d && d.kind === 'show' && artwork}
  <BackLink href="/tv">{t('nav.tv')}</BackLink>

  <Hero
    size="title"
    label={d.name}
    title={d.name}
    year={d.year}
    status={status.text}
    statusIcon={status.icon}
    backdropRef={d.backdropRef || undefined}
    {meta}
    blurb={d.overview || t('title.noOverview')}
    theme={heroTheme(d.name, d.kind)}
    minHeight={320}
    padY={24}
    blurbWidth={520}
    gap={16}
  >
    {#snippet art()}
      <ArtPanel art={artwork} />
    {/snippet}
  </Hero>

  <TitleToolbar
    detail={d}
    label={t('show.actionsFor', { title: d.name })}
    monitorLabel={t('show.watch')}
    askLabel={t('hero.askFor', { quality: t('ask.quality.uhd.short') })}
    onask={() => (askOpen = true)}
    onupdated={page.set}
  />

  <div class="cols">
    <div class="main">
      {#if seasonTabs.length > 1}
        <div class="seasons">
          <Tabs
            kind="nav"
            items={seasonTabs}
            value={season === undefined ? '' : String(season)}
            onchange={(id) => (picked = Number(id))}
            label={t('show.seasons')}
            prefix="show-seasons"
          />
        </div>
      {/if}
      <Panel labelledby={seasonId} padding="18px 20px 6px">
        <SectionHead
          title={season === 0 ? t('show.specials') : t('show.season', { n: season ?? 1 })}
          id={seasonId}
          align="center"
          gap={12}
        >
          <span class="note">
            {#if known}
              {t('show.haveOfTotal', { have, total: episodes.length })}
            {:else}
              {episodes.length === 1 ? t('show.episodeCountOne') : t('show.episodeCount', { n: episodes.length })}
            {/if}
          </span>
          <span class="head-actions">
            <PreviewTag />
            <Button size="compact" disabled>
              <Icon name="search" size={15} stroke={2.2} />
              {t('show.searchMissing')}
            </Button>
          </span>
        </SectionHead>
        {#if rows.length === 0}
          <p class="empty">{t('show.noEpisodes')}</p>
        {:else}
          <ol class="episodes">
            {#each rows as row (row.ep.id)}
              <li>
                <div class="name">
                  <span class="num" aria-hidden="true">{row.ep.number}</span>
                  <span class="label">
                    <span class="ep-title">{row.ep.name || t('show.episode', { n: row.ep.number })}</span>
                    {#if row.ep.name}<span class="ep-sub">{t('show.episode', { n: row.ep.number })}</span>{/if}
                  </span>
                </div>
                <div class="air">{formatAirDate(row.ep.airDate)}</div>
                <div class="status-col">
                  <div class="state {row.state}">
                    <Icon name={stateIcon[row.state]} size={16} stroke={2.2} />
                    <span>{row.text}</span>
                  </div>
                </div>
                <div class="ep-action">
                  {#if row.state === 'library'}
                    <Button size="compact" disabled aria-label={t('show.openAria', { n: row.ep.number, server: t('show.server') })}>
                      <Icon name="playOutline" size={15} stroke={2.2} />
                      {t('action.open')}
                    </Button>
                  {:else}
                    <Button size="compact" disabled aria-label={t('show.searchAria', { n: row.ep.number })}>
                      <Icon name="search" size={15} stroke={2.2} />
                      {t('action.search')}
                    </Button>
                  {/if}
                </div>
              </li>
            {/each}
          </ol>
        {/if}
      </Panel>
    </div>

    <div class="side">
      <Panel labelledby={rulesId} padding="18px 20px 16px">
        <SectionHead title={t('show.rules.heading')} id={rulesId} align="center" gap={0}>
          <PreviewTag />
          <Button variant="quiet" size="xs" disabled>{t('show.rules.change')}</Button>
        </SectionHead>
        <LabelValue
          items={[
            { label: t('show.rules.quality'), value: 'HD 1080p, fall back to 720p' },
            { label: t('show.rules.folder'), value: d.rootFolder || t('show.rules.folderDefault') },
            { label: t('show.rules.subtitles'), value: 'English' },
          ]}
          density="roomy"
          gap={12}
        />
        <p class="rules-note">{t('show.rules.note')}</p>
      </Panel>
    </div>
  </div>

  <AlsoInLibrary kind="show" exceptId={d.id} heading={t('show.moreInLibrary')} />

  <AskSheet
    bind:open={askOpen}
    title={d.name}
    target={{ titleId: d.id }}
    kind="show"
    defaultQuality="uhd"
  />
{:else if notFound}
  <TitleNotFound backHref="/tv" backLabel={t('nav.tv')} />
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton hero cards={2} />
{/if}

<style>
  .cols {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 14px;
    margin-bottom: 18px;
  }
  .main {
    flex: 2.4 1 560px;
    min-width: 0;
  }
  .side {
    flex: 1 1 300px;
    min-width: 0;
  }
  .seasons {
    margin-bottom: 14px;
  }

  .note {
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .head-actions {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .empty {
    padding-bottom: 14px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .episodes li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 16px;
    padding: 14px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .name {
    flex: 1 1 170px;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .num {
    flex: 0 0 28px;
    text-align: center;
    font-family: var(--font-serif);
    font-size: 28px;
    line-height: 1;
    color: var(--text);
  }
  .label {
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .ep-title {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .ep-sub {
    font-size: 12.5px;
    font-weight: 500;
    line-height: 16px;
    color: var(--text-3);
  }
  .air {
    flex: 0 0 116px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    font-variant-numeric: tabular-nums;
    color: var(--text-3);
  }
  .status-col {
    flex: 2 1 240px;
    min-width: 0;
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    box-sizing: border-box;
    font-size: 13.5px;
    font-weight: 500;
    line-height: 20px;
    color: var(--text);
  }
  .state.missing {
    padding: 3px 12px 3px 10px;
    border-radius: var(--radius-pill);
    background: var(--text);
    color: var(--ground);
    font-weight: 600;
  }
  .state.upcoming {
    padding: 2px 12px 2px 10px;
    border-radius: var(--radius-pill);
    border: 1px dashed var(--line-soft);
    color: var(--text-2);
  }
  .state.unknown {
    color: var(--text-3);
  }
  .ep-action {
    flex: 0 0 88px;
    width: 88px;
  }
  .ep-action :global(.btn) {
    width: 100%;
    padding: 0;
    gap: 6px;
  }
  .rules-note {
    margin-top: 12px;
    font-size: 13px;
    line-height: 19px;
    color: var(--text-3);
  }
</style>
