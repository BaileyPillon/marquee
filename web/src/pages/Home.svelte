<script lang="ts">
  import { onMount } from 'svelte';
  import ArtPanel from '../lib/ui/ArtPanel.svelte';
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import Button from '../lib/ui/Button.svelte';
  import ComingUp from '../lib/ui/ComingUp.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import type { IconName } from '../lib/ui/icons';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import PosterRow from '../lib/ui/PosterRow.svelte';
  import { horizonArt, keysArt, skylineArt } from '../lib/ui/art';
  import { listPath, listTitles, titlePath, type Title } from '../lib/core/catalog';
  import { formatDay, formatRuntime, kindLabel } from '../lib/core/format';
  import { t } from '../lib/core/i18n';
  import { libraryIndex } from '../lib/core/libraryIndex.svelte.ts';
  import { posterItem } from '../lib/core/posterItems';
  import { resource } from '../lib/core/resource.svelte.ts';
  import { session } from '../lib/core/session.svelte';
  import { heroTheme } from '../lib/core/themes';
  import { coming, friendsWant } from '../lib/mock/home';

  const role = $derived(session.me?.role);
  const canAdd = $derived(role === 'owner' || role === 'admin');

  // The catalog, newest first: the featured title and "Recently added" both come from it.
  const page = resource(() => 'home', (_key, signal) => listTitles({ sort: 'added', limit: 16 }, signal));
  const list = $derived(page.data);
  const titles = $derived(list?.items ?? []);

  onMount(() => libraryIndex.ensure());

  // The featured title: the newest one that has artwork (else simply the newest).
  const featured = $derived<Title | undefined>(
    titles.find((x) => x.backdropRef || x.posterRef) ?? titles[0],
  );
  const files = $derived(featured ? libraryIndex.titles.get(featured.id) : undefined);

  const status = $derived.by<{ text: string; icon: IconName }>(() => {
    if (!featured) return { text: '', icon: 'check' };
    if (files) {
      return {
        text: files.quality ? t('hero.inLibrary', { quality: files.quality }) : t('title.status.inLibrary'),
        icon: 'check',
      };
    }
    if (libraryIndex.status === 'ready') {
      return { text: featured.monitored ? t('title.status.waiting') : t('title.status.notMonitored'), icon: 'time' };
    }
    return { text: t('title.status.added', { date: formatDay(featured.addedAt) }), icon: 'check' };
  });
  const meta = $derived(
    featured
      ? [
          featured.kind === 'movie' ? '' : kindLabel(featured.kind),
          featured.year ? String(featured.year) : '',
          formatRuntime(featured.runtimeMin),
          featured.certification,
          featured.genres.slice(0, 2).join(', '),
        ]
          .filter(Boolean)
          .join(' · ') || kindLabel(featured.kind)
      : '',
  );
  // The same drawn art as the title's own page: skyline for a movie, horizon for a show, keys for music.
  const artwork = $derived(
    featured?.kind === 'show'
      ? horizonArt(featured.year)
      : featured?.kind === 'artist'
        ? keysArt(featured.year, 0, '')
        : skylineArt(featured?.year ?? 0),
  );

  const recent = $derived(titles.map(posterItem));
  const firstRun = $derived(!!list && list.total === 0);

  let askOpen = $state(false);
</script>

{#if featured}
  <Hero
    title={featured.name}
    year={featured.year}
    status={status.text}
    statusIcon={status.icon}
    backdropRef={featured.backdropRef || undefined}
    {meta}
    blurb={featured.overview || featured.tagline || t('title.noOverview')}
    theme={heroTheme(featured.name, featured.kind)}
  >
    {#snippet art()}
      <ArtPanel art={artwork} />
    {/snippet}
    {#snippet actions()}
      <Button variant="primary" href={titlePath(featured.kind, featured.id)}>
        <Icon name="arrow" size={18} stroke={2.2} />
        {t('hero.open')}
      </Button>
      {#if featured.kind !== 'artist'}
        <Button onclick={() => (askOpen = true)}>
          {t('hero.askFor', { quality: t('ask.quality.uhd.short') })}
        </Button>
      {/if}
    {/snippet}
  </Hero>

  <PosterRow
    title={t('home.recent')}
    href={listPath(featured.kind)}
    items={recent}
  />

  <PosterRow title={friendsWant.title} href={friendsWant.href} items={friendsWant.items} preview />
  <ComingUp events={coming} preview />

  <AskSheet
    bind:open={askOpen}
    title={featured.name}
    target={{ titleId: featured.id }}
    kind={featured.kind}
    defaultQuality="uhd"
    haveHd={!!files}
  />
{:else if firstRun}
  <!-- An empty catalog: the first thing to do is to add something. -->
  <section class="first" aria-labelledby="first-run-title">
    <Panel tag="div" padding="32px clamp(20px, 4vw, 44px)">
      <h1 id="first-run-title">{canAdd ? t('home.first.title') : t('home.first.memberTitle')}</h1>
      <p>{canAdd ? t('home.first.body') : t('home.first.memberBody')}</p>
      <div class="actions">
        <Button variant="primary" href="/explore">
          <Icon name="plus" size={18} stroke={2.2} />
          {canAdd ? t('home.first.add') : t('home.first.explore')}
        </Button>
        {#if role === 'owner'}
          <Button href="/settings?tab=folders">
            <Icon name="file" size={18} />
            {t('home.first.folders')}
          </Button>
        {/if}
      </div>
    </Panel>
  </section>
{:else if page.failed}
  <LoadFailed onretry={page.reload} />
{:else}
  <PageSkeleton hero cards={0} />
{/if}

<style>
  h1 {
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: clamp(32px, 5vw, 48px);
    line-height: 1.05;
    letter-spacing: 0.01em;
    color: var(--text);
  }
  p {
    max-width: 520px;
    margin-top: 12px;
    font-size: 15px;
    line-height: 22px;
    color: var(--text-2);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 22px;
  }
</style>
