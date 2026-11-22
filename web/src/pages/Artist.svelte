<script lang="ts">
  import { onMount } from 'svelte';
  import AlbumCard from '../lib/ui/AlbumCard.svelte';
  import AlsoInLibrary from '../lib/ui/AlsoInLibrary.svelte';
  import ArtPanel from '../lib/ui/ArtPanel.svelte';
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import BackLink from '../lib/ui/BackLink.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import DataTable, { type DataColumn } from '../lib/ui/DataTable.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import HistoryPanel from '../lib/ui/HistoryPanel.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import ReleasesPanel from '../lib/ui/ReleasesPanel.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import TitleNotFound from '../lib/ui/TitleNotFound.svelte';
  import TitleToolbar from '../lib/ui/TitleToolbar.svelte';
  import { keysArt } from '../lib/ui/art';
  import { ApiError } from '../lib/core/api';
  import { getTitle, titlePath, type Album } from '../lib/core/catalog';
  import { onEvent, onResync } from '../lib/core/events';
  import { formatDay, formatTrackLength, sentence } from '../lib/core/format';
  import { hydration } from '../lib/core/hydration.svelte.ts';
  import { t } from '../lib/core/i18n';
  import { libraryIndex } from '../lib/core/libraryIndex.svelte.ts';
  import { posterColor } from '../lib/core/posters';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { navigate, type Match } from '../lib/core/router';
  import { session } from '../lib/core/session.svelte';
  import { heroTheme } from '../lib/core/themes';
  import { trackFile } from '../lib/core/titleFiles';
  import { searchAlbum } from '../lib/core/subtitlesApi';
  import { watchTitleDownloads } from '../lib/core/titleDownloads.svelte';
  import { createToaster } from '../lib/core/toast.svelte.ts';
  import type { Status } from '../lib/core/types';
  import Toast from '../lib/ui/Toast.svelte';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const ALBUMS_SHOWN = 24;
  const TRACKS_SHOWN = 10;

  const page = resource(() => match.params['id'] ?? '', getTitle);
  const d = $derived(page.data);
  const notFound = $derived(page.error instanceof ApiError && page.error.status === 404);

  onMount(() => libraryIndex.ensure());

  $effect(() => {
    if (d && d.kind !== 'artist') navigate(titlePath(d.kind, d.id), { replace: true });
  });

  const firstTrack = $derived(d?.albums.find((a) => a.tracks.length > 0)?.tracks[0]);
  const known = $derived(firstTrack ? trackFile(firstTrack) !== undefined : libraryIndex.status === 'ready');
  const role = $derived(session.me?.role);
  const canManage = $derived(role === 'owner' || role === 'admin');

  // ---- albums that are still being fetched (adding an artist answers before they are in) ----
  const hydrating = $derived(!!d && d.albums.length === 0 && (d.hydrating === true || hydration.has(d.id)));

  /** Fetch the artist again without blanking the page. */
  async function refresh(): Promise<void> {
    if (!d) return;
    try {
      const fresh = await getTitle(String(d.id));
      page.set(fresh);
      if (!fresh.hydrating || fresh.albums.length > 0) hydration.done(fresh.id);
    } catch {
      // The next event or tick tries again.
    }
  }

  onMount(() => {
    const stop = onEvent('titles', (raw) => {
      const ev = raw as { id?: number; event?: string };
      if (ev.event === 'hydrated' && d && ev.id === d.id) void refresh();
    });
    const stopResync = onResync(() => void refresh());
    return () => {
      stop();
      stopResync();
    };
  });

  // The event may have come before this page was listening: while albums are loading, look again every few seconds.
  $effect(() => {
    if (!hydrating) return;
    const timer = window.setInterval(() => void refresh(), 4000);
    return () => window.clearInterval(timer);
  });

  // ---- albums ----
  const albums = $derived(
    d
      ? [...d.albums].sort(
          (a, b) => (a.year || 9999) - (b.year || 9999) || a.name.localeCompare(b.name),
        )
      : [],
  );
  // The server says which albums have a file (`inLibrary`); an older one leaves it out and the
  // owner's file list is joined instead.
  const albumsKnown = $derived(albums.some((a) => a.inLibrary !== undefined) || known);
  const inLibrary = (a: Album): boolean =>
    a.inLibrary ?? a.tracks.some((tr) => trackFile(tr)?.has === true);
  const haveAlbums = $derived(albumsKnown ? albums.filter(inLibrary).length : 0);
  let allAlbums = $state(false);
  const shownAlbums = $derived(allAlbums ? albums : albums.slice(0, ALBUMS_SHOWN));

  // Albums on their way in right now: a download of an artist carries its album's id in `episodeIds`.
  const live = watchTitleDownloads(
    () => d?.id,
    () => canManage,
  );
  const comingIn = $derived.by(() => {
    const map = new Map<number, number>();
    for (const item of live.items) {
      for (const id of item.episodeIds ?? []) map.set(id, Math.round(item.progress * 100));
    }
    return map;
  });

  /** In library, Downloading (with its percentage) or Missing, from what the server says. */
  function albumStatus(a: Album): Status {
    if (inLibrary(a) && albumsKnown) return { kind: 'library' };
    const pct = comingIn.get(a.id);
    if (pct !== undefined) return { kind: 'downloading', pct };
    return { kind: 'missing' };
  }

  // ---- looking for one album now ----
  const toaster = createToaster();
  let searching = $state<number | null>(null);

  async function findAlbum(a: Album): Promise<void> {
    if (!d || searching !== null) return;
    searching = a.id;
    try {
      const res = await searchAlbum(d.id, a.id);
      toaster.show(
        res.grabbed
          ? t('album.sent', { release: res.release?.title ?? a.name })
          : res.note || t('album.nothing'),
      );
      if (res.grabbed) void refresh();
    } catch (err) {
      toaster.show(err instanceof ApiError && err.status !== 0 && err.message ? err.message : t('album.failed'));
    } finally {
      searching = null;
    }
  }

  function trackCount(n: number): string {
    return t('artist.trackCount', { n });
  }

  // ---- tracks ----
  interface TrackRow {
    num: number;
    title: string;
    album: string;
    length: string;
    format: string;
  }
  const trackRows = $derived.by<TrackRow[]>(() => {
    const all = albums.flatMap((a) => a.tracks.map((tr) => ({ a, tr })));
    const listed = known ? all.filter(({ tr }) => trackFile(tr)?.has === true) : all;
    return listed.map(({ a, tr }, i) => {
      const file = trackFile(tr);
      return {
        num: i + 1,
        title: tr.name,
        album: a.name,
        length: formatTrackLength(tr.lengthMs),
        format: file?.quality || file?.format || '',
      };
    });
  });
  let allTracks = $state(false);
  const shownTracks = $derived(allTracks ? trackRows : trackRows.slice(0, TRACKS_SHOWN));

  const columns: DataColumn[] = [
    { id: 'num', label: t('artist.col.num') },
    { id: 'title', label: t('artist.col.title') },
    { id: 'album', label: t('artist.col.album') },
    { id: 'length', label: t('artist.col.length'), align: 'end' },
    { id: 'format', label: t('artist.col.format'), align: 'end' },
  ];
  const template = '40px minmax(240px,3fr) minmax(140px,1.4fr) 64px 96px';

  // ---- the header ----
  // The small capitals beside the staff have room for about 22 letters: two album names if they
  // fit, else the first one, cut short.
  const artCaption = $derived.by(() => {
    const names = albums.slice(0, 2).map((a) => a.name.toUpperCase());
    const both = names.join(' · ');
    const pick = both.length <= 22 ? both : (names[0] ?? '');
    return pick.length > 22 ? `${pick.slice(0, 21).trimEnd()}…` : pick;
  });
  const artwork = $derived(d ? keysArt(d.year, 0, artCaption) : undefined);
  const status = $derived.by(() => {
    if (!d) return { text: '', icon: 'check' as const };
    if (known) {
      return haveAlbums > 0
        ? {
            text: t('artist.status.library', { have: haveAlbums, total: albums.length }),
            icon: 'check' as const,
          }
        : { text: t('artist.status.none'), icon: 'time' as const };
    }
    return { text: t('title.status.added', { date: formatDay(d.addedAt) }), icon: 'check' as const };
  });
  const meta = $derived(
    d
      ? [d.genres.slice(0, 3).join(', '), d.year ? String(d.year) : '', sentence(d.status)]
          .filter(Boolean)
          .join(' · ') || t('kind.artist')
      : '',
  );

  let askOpen = $state(false);

  const uid = $props.id();
  const albumsId = `artist-albums-${uid}`;
  const tracksId = `artist-tracks-${uid}`;
</script>

{#if d && d.kind === 'artist' && artwork}
  <BackLink href="/music">{t('nav.music')}</BackLink>

  <Hero
    label={t('route.artist')}
    title={d.name}
    year={d.year}
    status={status.text}
    statusIcon={status.icon}
    backdropRef={d.backdropRef || undefined}
    {meta}
    blurb={d.overview || t('artist.noOverview')}
    theme={heroTheme(d.name, d.kind)}
    minHeight={310}
    metaTone="soft"
  >
    {#snippet art()}
      <ArtPanel art={artwork} />
    {/snippet}
  </Hero>

  <TitleToolbar
    detail={d}
    label={t('artist.actionsFor', { name: d.name })}
    monitorLabel={t('title.monitor')}
    askLabel={t('artist.askAll')}
    onask={() => (askOpen = true)}
    onupdated={page.set}
  />

  <section class="block" aria-labelledby={albumsId}>
    <SectionHead title={t('artist.albums')} id={albumsId}>
      <span class="note">
        {hydrating
          ? ''
          : albumsKnown
          ? t('artist.albumsHave', { have: haveAlbums, total: albums.length })
          : t('artist.albumsTotal', { n: albums.length })}
      </span>
    </SectionHead>
    {#if albums.length === 0 && hydrating}
      <p class="empty" role="status" aria-busy="true">{t('artist.loadingAlbums')}</p>
    {:else if albums.length === 0}
      <p class="empty">{t('artist.noAlbums')}</p>
    {:else}
      <div class="albums">
        {#each shownAlbums as album (album.id)}
          {@const status = albumStatus(album)}
          <div class="album">
            <AlbumCard
              title={album.name}
              years={album.year ? String(album.year) : ''}
              {status}
              color={posterColor(album.name)}
              caption={album.type === 'ep' ? t('artist.type.ep') : t('artist.type.album')}
              detail={[
                album.tracks.length > 0 ? trackCount(album.tracks.length) : '',
                album.monitored ? '' : t('album.notMonitored'),
              ]
                .filter(Boolean)
                .join(' · ')}
              imageRef={album.coverRef || undefined}
            />
            {#if canManage && status.kind === 'missing'}
              <div class="album-action">
                <Button
                  size="compact"
                  disabled={searching !== null}
                  aria-label={t('album.searchAria', { name: album.name })}
                  onclick={() => findAlbum(album)}
                >
                  <Icon name="search" size={15} stroke={2.2} />
                  {searching === album.id ? t('album.searching') : t('album.search')}
                </Button>
              </div>
            {/if}
          </div>
        {/each}
      </div>
      {#if albums.length > ALBUMS_SHOWN}
        <div class="more">
          <Button onclick={() => (allAlbums = !allAlbums)}>
            {allAlbums ? t('artist.fewerAlbums') : t('artist.allAlbums', { n: albums.length })}
          </Button>
        </div>
      {/if}
    {/if}
  </section>

  <section class="block" aria-labelledby={tracksId}>
    <SectionHead title={known ? t('artist.tracks.heading') : t('artist.tracks.headingAll')} id={tracksId} />
    {#if trackRows.length === 0}
      <Panel tag="div" padding="18px">
        <p class="empty tight">{known ? t('artist.noTracks') : t('artist.noTracksYet')}</p>
      </Panel>
    {:else}
      <Panel tag="div" padding="0" clip>
        <DataTable
          label={known ? t('artist.tracks.heading') : t('artist.tracks.headingAll')}
          {columns}
          {template}
          minWidth={700}
          variant="inset"
          padX={18}
          rowMin={52}
          rows={shownTracks}
          rowKey={(row) => row.num}
        >
          {#snippet cell(row: TrackRow, column: DataColumn)}
            {#if column.id === 'num'}
              <span class="track-num">{row.num}</span>
            {:else if column.id === 'title'}
              <span class="track-title">{row.title}</span>
            {:else if column.id === 'album'}
              <span class="track-album">{row.album}</span>
            {:else if column.id === 'length'}
              <span class="track-length">{row.length}</span>
            {:else if row.format}
              <Chip size="format">{row.format}</Chip>
            {:else}
              <span class="track-length">—</span>
            {/if}
          {/snippet}
        </DataTable>
        <div class="foot">
          <span class="foot-note">
            {known
              ? t('artist.showing', { shown: shownTracks.length, total: trackRows.length })
              : t('artist.showingAll', { shown: shownTracks.length, total: trackRows.length })}
          </span>
          {#if trackRows.length > TRACKS_SHOWN}
            <button type="button" class="foot-link" onclick={() => (allTracks = !allTracks)}>
              {allTracks ? t('artist.fewerTracks') : t('artist.seeAllTracks')}
            </button>
          {/if}
        </div>
      </Panel>
    {/if}
  </section>

  {#if canManage}
    <div class="block">
      <ReleasesPanel titleId={d.id} name={d.name} kind="artist" quality="hd" />
    </div>
  {/if}

  <div class="block"><HistoryPanel titleId={d.id} /></div>

  <AlsoInLibrary kind="artist" exceptId={d.id} heading={t('artist.moreInLibrary')} />

  <AskSheet bind:open={askOpen} title={d.name} target={{ titleId: d.id }} kind="artist" />
{:else if notFound}
  <TitleNotFound backHref="/music" backLabel={t('nav.music')} />
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton hero cards={2} />
{/if}

<Toast message={toaster.message} />

<style>
  .block {
    margin-bottom: 26px;
  }
  .note {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .empty {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .albums {
    display: grid;
    /* 172px covers on desktop; on a phone two to a row. */
    grid-template-columns: repeat(auto-fill, minmax(min(172px, calc(50% - 7px)), 1fr));
    gap: 16px 14px;
  }
  .more {
    margin-top: 16px;
  }
  .album {
    min-width: 0;
  }
  .album-action {
    margin-top: 8px;
  }

  .track-num {
    font-size: 13.5px;
    font-weight: 500;
    line-height: 20px;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }
  .track-title {
    display: block;
    overflow: hidden;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .track-album {
    display: block;
    overflow: hidden;
    font-size: 13.5px;
    font-weight: 500;
    line-height: 20px;
    color: var(--text-2);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .track-length {
    font-size: 13.5px;
    font-weight: 500;
    line-height: 20px;
    font-variant-numeric: tabular-nums;
    color: var(--text-2);
  }
  .foot {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    box-sizing: border-box;
    padding: 12px 18px;
    border-top: 1px solid var(--hairline-strong);
  }
  .foot-note {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .foot-link {
    padding: 0;
    border: 0;
    background: transparent;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .foot-link:hover {
    color: var(--text);
  }
</style>
