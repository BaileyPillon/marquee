<script lang="ts">
  import type { Snippet } from 'svelte';
  import HeroArt from './HeroArt.svelte';
  import Icon from './Icon.svelte';
  import { t } from '../core/i18n';
  import type { HeroTheme } from '../core/types';

  interface Props {
    title: string;
    year: number;
    /** Text after the check mark, e.g. "In your library · 1080p". */
    status: string;
    /** One line under the title: year, kind, credit. */
    meta: string;
    blurb: string;
    theme: HeroTheme;
    /** home: 44 to 64px title. title: 48 to 80px, for a title's own page. */
    size?: 'home' | 'title';
    actions?: Snippet;
    chips?: Snippet;
  }

  let { title, year, status, meta, blurb, theme, size = 'home', actions, chips }: Props = $props();
</script>

<div class="wrap">
  <section
    class="hero {size}"
    aria-label={t('hero.label')}
    style:--hero-bg={theme.bg}
    style:--hero-art-bg={theme.artBg}
    style:--hero-disc={theme.disc}
    style:--hero-tower-a={theme.towerA}
    style:--hero-tower-b={theme.towerB}
  >
    <div class="text">
      <div class="status">
        <Icon name="check" size={16} stroke={2.2} />
        <span>{status}</span>
      </div>
      <h1>{title}</h1>
      <div class="rule"></div>
      <div class="meta">{meta}</div>
      <p class="blurb">{blurb}</p>
      {#if chips}<div class="chips">{@render chips()}</div>{/if}
      {#if actions}<div class="actions">{@render actions()}</div>{/if}
    </div>
    <div class="art" aria-hidden="true">
      <HeroArt {year} />
    </div>
  </section>
</div>

<style>
  .wrap {
    container-type: inline-size;
    margin-bottom: 22px;
  }
  .hero {
    position: relative;
    display: flex;
    flex-wrap: wrap;
    min-height: 290px;
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
    padding: 22px clamp(20px, 3vw, 40px);
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
  .blurb {
    max-width: 480px;
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
    min-height: 290px;
    overflow: hidden;
    background: var(--hero-art-bg);
  }

  /* Text and art no longer fit side by side: the art becomes a full-width band under the text. */
  @container (max-width: 979.98px) {
    .art {
      flex: 1 0 100%;
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
      margin-bottom: 18px;
    }
  }
</style>
