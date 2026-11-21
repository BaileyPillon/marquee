<script lang="ts">
  import Badge from './Badge.svelte';
  import { posterColor, posterTitleSize, type PosterSize } from '../core/posters';
  import { statusLabel, statusTone } from '../core/status';
  import type { Status } from '../core/types';

  interface Props {
    title: string;
    year: string;
    status: Status;
    /** md 150 x 188 (Home), sm 108 x 132 (phone rows), mini 96 x 120 (Requests). */
    size?: PosterSize;
  }

  let { title, year, status, size = 'md' }: Props = $props();

  const color = $derived(posterColor(title));
  const fontSize = $derived(posterTitleSize(title, size));
</script>

<div class="poster {size}" style:background={color}>
  <div class="top">
    <Badge tone={statusTone(status)} size={size === 'mini' ? 'xs' : 'sm'}>{statusLabel(status)}</Badge>
  </div>
  <div class="body">
    <div class="rule"></div>
    <div class="title" style:font-size="{fontSize}px">{title}</div>
    <div class="year">{year}</div>
  </div>
  {#if status.kind === 'downloading'}
    <div class="progress" aria-hidden="true"><div style:width="{status.pct}%"></div></div>
  {/if}
</div>

<style>
  .poster {
    position: relative;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    overflow: hidden;
    border-radius: var(--radius-sm);
    box-shadow: inset 0 0 0 1px var(--poster-edge);
  }
  .md {
    width: 150px;
    height: 188px;
    padding: 11px 11px 12px;
  }
  .sm {
    width: 108px;
    height: 132px;
    padding: 10px;
  }
  .mini {
    width: 96px;
    height: 120px;
    padding: 8px 8px 9px;
  }
  .top {
    display: flex;
  }
  .rule {
    width: 28px;
    height: 1px;
    margin-bottom: 8px;
    background: var(--line-strong);
  }
  .mini .rule {
    width: 24px;
    margin-bottom: 6px;
  }
  .title {
    font-family: var(--font-serif);
    font-weight: 400;
    line-height: 1.04;
    color: var(--text);
  }
  .year {
    margin-top: 6px;
    font-size: 12px;
    font-weight: 500;
    line-height: 16px;
    letter-spacing: 0.04em;
    color: var(--text);
  }
  .mini .year {
    margin-top: 4px;
    font-size: 11px;
    line-height: 14px;
  }
  .progress {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 5px;
    background: var(--track);
  }
  .progress div {
    height: 5px;
    background: var(--text);
  }
</style>
