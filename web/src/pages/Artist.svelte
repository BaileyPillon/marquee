<script lang="ts">
  import AlbumCard from '../lib/ui/AlbumCard.svelte';
  import ArtPanel from '../lib/ui/ArtPanel.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import DataTable, { type DataColumn } from '../lib/ui/DataTable.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import SearchField from '../lib/ui/SearchField.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import Toast from '../lib/ui/Toast.svelte';
  import { keysArt } from '../lib/ui/art';
  import { t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { resource } from '../lib/core/resource.svelte.ts';
  import type { Match } from '../lib/core/router';
  import { createToaster } from '../lib/core/toast.svelte.ts';
  import type { Track } from '../lib/core/views';
  import { getArtist } from '../lib/mock/artist';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const page = resource(() => match.params['id'] ?? '', getArtist);
  const view = $derived(page.data);
  const artwork = $derived(view ? keysArt(view.born, view.died, view.artCaption) : undefined);

  // Asking for a whole discography waits for the owner's yes, like any ask. The API comes later.
  const toaster = createToaster();

  const columns: DataColumn[] = [
    { id: 'num', label: t('artist.col.num') },
    { id: 'title', label: t('artist.col.title') },
    { id: 'album', label: t('artist.col.album') },
    { id: 'length', label: t('artist.col.length'), align: 'end' },
    { id: 'format', label: t('artist.col.format'), align: 'end' },
  ];
  const template = '40px minmax(240px,3fr) minmax(140px,1.4fr) 64px 96px';

  const uid = $props.id();
  const albumsId = `artist-albums-${uid}`;
  const tracksId = `artist-tracks-${uid}`;
  const similarId = `artist-similar-${uid}`;

  function trackCount(n: number): string {
    return n === 1 ? t('artist.trackCountOne', { n }) : t('artist.trackCount', { n });
  }

  function initials(name: string): string {
    return name
      .split(' ')
      .map((word) => word.charAt(0))
      .join('');
  }
</script>

<div class="top">
  {#if $isDesktop}
    <div class="field"><SearchField /></div>
  {/if}
  <Button
    variant="primary"
    pill
    onclick={() => toaster.show(t('ask.sent', { owner: view?.ownerName ?? '' }))}
  >
    <Icon name="plus" size={18} stroke={2} />
    {t('artist.askAll')}
  </Button>
</div>

{#if view && artwork}
  <Hero
    label={t('route.artist')}
    title={view.name}
    year={view.born}
    status={view.status}
    meta={view.meta}
    blurb={view.blurb}
    theme={view.theme}
    minHeight={310}
    metaTone="soft"
  >
    {#snippet art()}
      <ArtPanel art={artwork} />
    {/snippet}
    {#snippet actions()}
      <Button size="sm">{t('artist.more', { name: view.shortName })}</Button>
    {/snippet}
  </Hero>

  <section class="block" aria-labelledby={albumsId}>
    <SectionHead title={t('artist.albums')} id={albumsId}>
      <span class="note">{view.albums.note}</span>
    </SectionHead>
    <div class="albums">
      {#each view.albums.items as album (album.id)}
        <AlbumCard
          href="/music"
          title={album.title}
          years={album.years}
          status={album.status}
          color={album.color}
          caption={album.caption}
          detail={trackCount(album.tracks)}
        />
      {/each}
    </div>
  </section>

  <section class="block" aria-labelledby={tracksId}>
    <SectionHead title={t('artist.tracks.heading')} id={tracksId}>
      <button type="button" class="soft-link">{t('artist.openIn', { server: view.server })}</button>
    </SectionHead>
    <Panel tag="div" padding="0" clip>
      <DataTable
        label={t('artist.tracks.heading')}
        {columns}
        {template}
        minWidth={700}
        variant="inset"
        padX={18}
        rowMin={52}
        rows={view.tracks.items}
        rowKey={(row) => row.num}
      >
        {#snippet cell(row: Track, column: DataColumn)}
          {#if column.id === 'num'}
            <span class="track-num">{row.num}</span>
          {:else if column.id === 'title'}
            <a class="track-title" href="/music">{row.title}</a>
          {:else if column.id === 'album'}
            <span class="track-album">{row.album}</span>
          {:else if column.id === 'length'}
            <span class="track-length">{row.length}</span>
          {:else}
            <Chip size="format">{row.format}</Chip>
          {/if}
        {/snippet}
      </DataTable>
      <div class="foot">
        <span class="foot-note">
          {t('artist.showing', { shown: view.tracks.shown, total: view.tracks.total })}
        </span>
        <a class="foot-link" href="/music">{t('artist.seeAllTracks')}</a>
      </div>
    </Panel>
  </section>

  <section aria-labelledby={similarId}>
    <SectionHead title={t('artist.similar')} id={similarId} />
    <ul class="similar">
      {#each view.similar as artist (artist.id)}
        <li>
          <a class="person" href="/music/artists/1">
            <span class="avatar" aria-hidden="true" style:background={artist.color}>
              {initials(artist.name)}
            </span>
            <span class="person-name">{artist.name}</span>
            <span class="person-detail">{artist.detail}</span>
          </a>
        </li>
      {/each}
    </ul>
  </section>
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton hero cards={2} />
{/if}

<Toast message={toaster.message} />

<style>
  .top {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    margin-bottom: 16px;
  }
  .field {
    flex: 1 1 320px;
    max-width: 520px;
  }

  .block {
    margin-bottom: 26px;
  }
  .note {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .albums {
    display: grid;
    /* 172px covers on desktop; on a phone two to a row. */
    grid-template-columns: repeat(auto-fill, minmax(min(172px, calc(50% - 7px)), 1fr));
    gap: 16px 14px;
  }

  .soft-link {
    padding: 0;
    border: 0;
    background: transparent;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  .soft-link:hover {
    color: var(--text);
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
    text-decoration: none;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .track-title:hover {
    color: var(--accent);
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
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
    text-decoration: none;
  }
  .foot-link:hover {
    color: var(--text);
  }

  .similar {
    display: flex;
    gap: 24px;
    overflow-x: auto;
    padding-bottom: 4px;
    scrollbar-width: thin;
    scrollbar-color: var(--input-border) transparent;
  }
  .person {
    flex: 0 0 112px;
    width: 112px;
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    text-decoration: none;
    color: var(--text);
  }
  .person:hover {
    color: var(--text);
  }
  .avatar {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 92px;
    height: 92px;
    border-radius: 50%;
    box-shadow: inset 0 0 0 1px var(--poster-edge);
    font-family: var(--font-serif);
    font-size: 34px;
    line-height: 1;
    letter-spacing: 0.02em;
    color: var(--text);
  }
  .person-name {
    margin-top: 10px;
    font-size: 14px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
  }
  .person-detail {
    font-size: 12.5px;
    font-weight: 500;
    line-height: 17px;
    color: var(--text-3);
  }
</style>
