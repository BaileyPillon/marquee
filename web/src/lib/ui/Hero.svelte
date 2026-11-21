<script lang="ts">
  import type { Snippet } from 'svelte';
  import ArtImage from './ArtImage.svelte';
  import HeroArt from './HeroArt.svelte';
  import Icon from './Icon.svelte';
  import type { IconName } from './icons';
  import { t } from '../core/i18n';
  import type { PanelTheme } from '../core/types';

  interface Props {
    title: string;
    year: number;
    /** Text after the check mark, e.g. "In your library · 1080p". */
    status: string;
    /** The mark before the status: a check when it is in the library, a clock when it is not yet. */
    statusIcon?: IconName;
    /**
     * The real backdrop (an artwork ref). It lies over the drawn art on the art side, under a dark
     * scrim; without it, or if it cannot load, the drawn art shows.
     */
    backdropRef?: string;
    /** One line under the title: year, kind, credit. */
    meta: string;
    blurb: string;
    theme: PanelTheme;
    /** home: 44 to 64px title. title: 48 to 80px, for a title's own page. */
    size?: 'home' | 'title';
    actions?: Snippet;
    chips?: Snippet;
    /** Replaces the skyline: an <ArtPanel> with the title's own composition. */
    art?: Snippet;
    /** Name of the region. Defaults to "Featured title". */
    label?: string;
    /** Minimum height of the panel in px. */
    minHeight?: number;
    /** Top and bottom padding of the text column in px. */
    padY?: number;
    /** Widest the blurb may be, in px. */
    blurbWidth?: number;
    /** Space under the panel in px. */
    gap?: number;
    /** The meta line: soft is a little lighter, for a panel with a pale ground. */
    metaTone?: 'default' | 'soft';
  }

  let {
    title,
    year,
    status,
    statusIcon = 'check',
    backdropRef,
    meta,
    blurb,
    theme,
    size = 'home',
    actions,
    chips,
    art,
    label,
    minHeight = 290,
    padY = 22,
    blurbWidth = 480,
    gap = 22,
    metaTone = 'default',
  }: Props = $props();

  let backdropLoaded = $state(false);
</script>

<div class="wrap" style:--hero-gap="{gap}px">
  <section
    class="hero {size}"
    aria-label={label ?? t('hero.label')}
    style:--hero-min={`${minHeight}px`}
    style:--hero-pad-y={`${padY}px`}
    style:--hero-blurb={`${blurbWidth}px`}
    style:--hero-bg={theme.bg}
    style:--hero-art-bg={theme.artBg}
    style:--hero-disc={theme.disc}
    style:--hero-tower-a={theme.towerA}
    style:--hero-tower-b={theme.towerB}
  >
    <div class="text">
      <div class="status">
        <Icon name={statusIcon} size={16} stroke={2.2} />
        <span>{status}</span>
      </div>
      <h1>{title}</h1>
      <div class="rule"></div>
      <div class="meta" class:soft={metaTone === 'soft'}>{meta}</div>
      <p class="blurb">{blurb}</p>
      {#if chips}<div class="chips">{@render chips()}</div>{/if}
      {#if actions}<div class="actions">{@render actions()}</div>{/if}
    </div>
    <div class="art" aria-hidden="true">
      {#if art}{@render art()}{:else}<HeroArt {year} />{/if}
      {#if backdropRef}
        <ArtImage imageRef={backdropRef} bind:loaded={backdropLoaded} />
        <div class="scrim" class:on={backdropLoaded}></div>
      {/if}
    </div>
  </section>
</div>

<style>
  .wrap {
    container-type: inline-size;
    margin-bottom: var(--hero-gap);
  }
  .hero {
    position: relative;
    display: flex;
    flex-wrap: wrap;
    min-height: var(--hero-min);
    overflow: hidden;
    border-radius: var(--radius-lg);
    background: var(--hero-bg);
  }
  .text {
    flex: 1 1 460px;
    min-width: 0;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    justify-content: center;
    padding: var(--hero-pad-y) clamp(20px, 3vw, 40px);
  }
  .status {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text);
  }
  h1 {
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: clamp(44px, 6vw, 64px);
    line-height: 1;
    letter-spacing: 0.01em;
    color: var(--text);
  }
  .title h1 {
    font-size: clamp(48px, 6.5vw, 80px);
  }
  .rule {
    width: 56px;
    height: 1px;
    margin: 10px 0;
    background: var(--line);
  }
  .meta {
    margin-bottom: 8px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
  }
  .meta.soft {
    color: var(--text-2);
  }
  .blurb {
    max-width: var(--hero-blurb);
    font-size: 14.5px;
    font-weight: 400;
    line-height: 21px;
    color: var(--text-soft);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 16px;
  }
  .art {
    position: relative;
    flex: 0 0 520px;
    max-width: 100%;
    min-height: var(--hero-min);
    overflow: hidden;
    background: var(--hero-art-bg);
  }

  /*
   * The dark scrim over a real backdrop: it melts the picture into the panel on the text side and
   * darkens the rest, so nothing drawn or written near it loses contrast. No text sits on the art
   * (the art side is decorative and the text column is its own box), and the panel colour under the
   * text is unchanged.
   */
  .scrim {
    position: absolute;
    inset: 0;
    opacity: 0;
    transition: opacity 0.3s ease;
    background:
      linear-gradient(90deg, var(--hero-bg) 0%, rgba(19, 18, 17, 0.55) 38%, rgba(19, 18, 17, 0.4) 100%);
  }
  .scrim.on {
    opacity: 1;
  }

  /* Text and art no longer fit side by side: the art becomes a full-width band under the text. */
  @container (max-width: 979.98px) {
    .art {
      flex: 1 0 100%;
    }
    .scrim {
      background:
        linear-gradient(180deg, var(--hero-bg) 0%, rgba(19, 18, 17, 0.5) 40%, rgba(19, 18, 17, 0.4) 100%);
    }
  }

  /* Phone: no art. A framed colour card with full-width buttons, as on the phone Home. */
  @container (max-width: 599.98px) {
    .hero {
      min-height: 0;
      border-radius: var(--radius-xl);
      box-shadow: inset 0 0 0 1px var(--poster-edge);
    }
    .hero::after {
      content: '';
      position: absolute;
      inset: 7px;
      border: 1px solid rgba(242, 237, 228, 0.18);
      border-radius: var(--radius-md);
      pointer-events: none;
    }
    .art {
      display: none;
    }
    .text {
      padding: 16px;
    }
    .actions {
      flex-direction: column;
      gap: 10px;
      margin-top: 12px;
    }
    .actions :global(.btn) {
      width: 100%;
      height: 48px;
      border-radius: var(--radius-lg);
      font-size: 16px;
    }
  }

  @media (max-width: 899.98px) {
    .wrap {
      margin-bottom: min(var(--hero-gap), 18px);
    }
  }
</style>
