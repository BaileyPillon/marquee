<script lang="ts">
  import ArtImage from './ArtImage.svelte';
  import Badge from './Badge.svelte';
  import { titleSize } from '../core/posters';
  import { t } from '../core/i18n';
  import { statusLabel, statusTone } from '../core/status';
  import type { Status } from '../core/types';

  interface Props {
    /** Where the card leads; without it the card is not a link (there is no album page yet). */
    href?: string;
    title: string;
    /** Years recorded, e.g. "1830–1846". */
    years: string;
    status: Status;
    /** CSS colour of the cover. */
    color: string;
    /** Under the cover: where the recording comes from. */
    caption: string;
    /** Under the caption: how many tracks. */
    detail: string;
    /** The real cover (an artwork ref), over the typographic cover. */
    imageRef?: string;
  }

  let { href, title, years, status, color, caption, detail, imageRef }: Props = $props();

  // Albums are not "requested": the artist's page says they are wanted.
  const label = $derived(status.kind === 'requested' ? t('status.wanted') : statusLabel(status));
  // A long real title gets a smaller size so four lines still fit the square cover.
  const fontSize = $derived(title.length > 26 ? 17 : titleSize(title) + 3);
</script>

<svelte:element this={href ? 'a' : 'div'} class="card" {href}>
  <div class="cover" style:background={color}>
    <div class="ring big" aria-hidden="true"></div>
    <div class="ring small" aria-hidden="true"></div>
    <div class="body">
      <div class="rule"></div>
      <div class="title" style:font-size="{fontSize}px">{title}</div>
      <div class="years">{years}</div>
    </div>
    {#if imageRef}<ArtImage {imageRef} />{/if}
    <div class="top"><Badge tone={statusTone(status)}>{label}</Badge></div>
    {#if status.kind === 'downloading'}
      <div class="progress" aria-hidden="true"><div style:width="{status.pct}%"></div></div>
    {/if}
  </div>
  <div class="caption">{caption}</div>
  <div class="detail">{detail}</div>
</svelte:element>

<style>
  .card {
    display: block;
    min-width: 0;
    color: inherit;
    text-decoration: none;
  }
  .card:hover {
    color: inherit;
  }
  .cover {
    position: relative;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
    width: 100%;
    aspect-ratio: 1 / 1;
    padding: 12px;
    overflow: hidden;
    border-radius: var(--radius-sm);
    box-shadow: inset 0 0 0 1px var(--poster-edge);
  }
  .ring {
    position: absolute;
    box-sizing: border-box;
    border-radius: 50%;
  }
  .big {
    inset-inline-end: -40px;
    top: -40px;
    width: 120px;
    height: 120px;
    border: 1px solid rgba(242, 237, 228, 0.12);
  }
  .small {
    inset-inline-end: -20px;
    top: -20px;
    width: 80px;
    height: 80px;
    border: 1px solid rgba(242, 237, 228, 0.18);
  }
  /* The badge and the bar sit above the cover art; the type beneath it. */
  .top {
    position: absolute;
    inset-inline-start: 12px;
    top: 12px;
    z-index: 2;
    display: flex;
  }
  .body {
    position: relative;
  }
  .rule {
    width: 28px;
    height: 1px;
    margin-bottom: 8px;
    background: var(--line-strong);
  }
  .title {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 4;
    line-clamp: 4;
    overflow: hidden;
    overflow-wrap: break-word;
    font-family: var(--font-serif);
    font-weight: 400;
    line-height: 1.04;
    color: var(--text);
  }
  .years {
    margin-top: 6px;
    font-size: 12px;
    font-weight: 500;
    line-height: 16px;
    letter-spacing: 0.04em;
    color: var(--text);
  }
  .progress {
    position: absolute;
    inset-inline: 0;
    bottom: 0;
    z-index: 2;
    height: 5px;
    background: var(--track);
  }
  .progress div {
    height: 5px;
    background: var(--text);
  }
  .caption {
    margin-top: 8px;
    min-height: 16px;
    font-size: 12.5px;
    font-weight: 500;
    line-height: 16px;
    color: var(--text-ask);
  }
  .detail {
    font-size: 12.5px;
    font-weight: 500;
    line-height: 16px;
    color: var(--muted);
  }
</style>
