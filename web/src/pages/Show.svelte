<script lang="ts">
  import ArtPanel from '../lib/ui/ArtPanel.svelte';
  import BackLink from '../lib/ui/BackLink.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import type { IconName } from '../lib/ui/icons';
  import LabelValue from '../lib/ui/LabelValue.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import PageSkeleton from '../lib/ui/PageSkeleton.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import PosterRow from '../lib/ui/PosterRow.svelte';
  import ProgressBar from '../lib/ui/ProgressBar.svelte';
  import SectionHead from '../lib/ui/SectionHead.svelte';
  import Toggle from '../lib/ui/Toggle.svelte';
  import { horizonArt } from '../lib/ui/art';
  import { t } from '../lib/core/i18n';
  import { resource } from '../lib/core/resource.svelte.ts';
  import type { Match } from '../lib/core/router';
  import type { EpisodeState } from '../lib/core/views';
  import { getShow } from '../lib/mock/show';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const page = resource(() => match.params['id'] ?? '', getShow);
  const view = $derived(page.data);

  // Following a show is a switch the person can flip; the server will keep it later.
  let watchChoice = $state<boolean | undefined>();
  const watching = $derived(watchChoice ?? view?.watching ?? false);

  const artwork = $derived(view ? horizonArt(view.year) : undefined);

  const stateIcon: Record<EpisodeState, IconName> = {
    library: 'check',
    downloading: 'downloads',
    missing: 'search',
    upcoming: 'time',
  };

  const uid = $props.id();
  const seasonId = `show-season-${uid}`;
  const rulesId = `show-rules-${uid}`;
</script>

{#if view && artwork}
  <BackLink href="/tv">{t('nav.tv')}</BackLink>

  <Hero
    size="title"
    label={view.title}
    title={view.title}
    year={view.year}
    status={view.status}
    meta={view.meta}
    blurb={view.blurb}
    theme={view.theme}
    minHeight={320}
    padY={24}
    blurbWidth={520}
    gap={16}
  >
    {#snippet art()}
      <ArtPanel art={artwork} />
    {/snippet}
  </Hero>

  <div class="actions" role="group" aria-label={t('show.actionsFor', { title: view.title })}>
    <Button variant="primary">
      <Icon name="play" size={18} filled />
      {t('hero.openIn', { server: view.server })}
    </Button>
    <Toggle checked={watching} label={t('show.watch')} onchange={(next) => (watchChoice = next)} />
    <Button>
      <Icon name="search" size={18} stroke={2} />
      {t('show.searchMissing')}
    </Button>
  </div>

  <div class="cols">
    <div class="main">
      <Panel labelledby={seasonId} padding="18px 20px 6px">
        <SectionHead title={t('show.season', { n: view.season.number })} id={seasonId} gap={12}>
          <span class="note">
            {t('show.haveOfTotal', { have: view.season.have, total: view.season.total })}
          </span>
        </SectionHead>
        <ol class="episodes">
          {#each view.season.episodes as ep (ep.num)}
            <li>
              <div class="name">
                <span class="num" aria-hidden="true">{ep.num}</span>
                <span class="label">{t('show.episode', { n: ep.num })}</span>
              </div>
              <div class="air">{ep.air}</div>
              <div class="status-col">
                <div class="state {ep.state}">
                  <Icon name={stateIcon[ep.state]} size={16} stroke={2.2} />
                  <span>{ep.status}</span>
                </div>
                {#if ep.state === 'downloading' && ep.progress !== undefined}
                  <div class="bar"><ProgressBar value={ep.progress} height={4} /></div>
                {/if}
              </div>
              <div class="ep-action">
                {#if ep.action === 'open'}
                  <Button
                    size="compact"
                    aria-label={t('show.openAria', { n: ep.num, server: view.server })}
                  >
                    <Icon name="playOutline" size={15} stroke={2.2} />
                    {t('action.open')}
                  </Button>
                {:else}
                  <Button size="compact" aria-label={t('show.searchAria', { n: ep.num })}>
                    <Icon name="search" size={15} stroke={2.2} />
                    {t('action.search')}
                  </Button>
                {/if}
              </div>
            </li>
          {/each}
        </ol>
      </Panel>
    </div>

    <div class="side">
      <Panel labelledby={rulesId} padding="18px 20px 16px">
        <SectionHead title={t('show.rules.heading')} id={rulesId} align="center" gap={0}>
          <Button variant="quiet" size="xs">{t('show.rules.change')}</Button>
        </SectionHead>
        <LabelValue items={view.rules} density="roomy" gap={12} />
        <p class="rules-note">{t('show.rules.note')}</p>
      </Panel>
    </div>
  </div>

  <PosterRow title={t('show.similar')} href="/tv" items={view.similar} />
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

  .note {
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
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
    flex: 1 1 150px;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .num {
    flex: 0 0 24px;
    text-align: center;
    font-family: var(--font-serif);
    font-size: 28px;
    line-height: 1;
    color: var(--text);
  }
  .label {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .air {
    flex: 0 0 100px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    font-variant-numeric: tabular-nums;
    color: var(--text-3);
  }
  .status-col {
    flex: 2 1 300px;
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
  .bar {
    width: 240px;
    max-width: 100%;
    margin-top: 8px;
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
