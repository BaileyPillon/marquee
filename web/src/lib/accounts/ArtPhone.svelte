<script lang="ts">
  import { t } from '../core/i18n';
  import { toRoman } from '../ui/heroArt';
  import { buildPhoneArt } from './authArt';

  // The scene is fixed; build it once per instance.
  const shapes = buildPhoneArt();
  const year = toRoman(new Date().getFullYear());

  // The scene is drawn on a 390 x 300 stage. Phones are 360 to 430 wide, so scale the stage to
  // the screen's width (up to 480) instead of cropping or letterboxing it.
  const STAGE_W = 390;
  const STAGE_H = 300;
  let width = $state(0);
  const scale = $derived(width ? Math.min(width, 480) / STAGE_W : 1);
</script>

<div class="art" style:height="{STAGE_H * scale}px" bind:clientWidth={width} aria-hidden="true">
  <div class="stage" style:transform="scale({scale})">
    {#each shapes as style, i (i)}
      <div {style}></div>
    {/each}
    <div class="words">
      <div class="title">{t('auth.art.title')}</div>
      <div class="dash"></div>
      <div class="admit">{t('auth.art.admit')}</div>
    </div>
    <div class="year">
      <div class="year-rule"></div>
      <div class="numeral">{year}</div>
      <div class="year-rule"></div>
    </div>
  </div>
</div>

<style>
  .art {
    position: relative;
    flex: 0 0 auto;
    box-sizing: border-box;
    overflow: hidden;
    background: var(--art-bg);
    border-bottom: 1px solid var(--hairline);
  }
  .stage {
    position: absolute;
    left: 50%;
    top: 0;
    width: 390px;
    height: 300px;
    margin-left: -195px;
    transform-origin: 50% 0;
  }
  .words {
    position: absolute;
    left: 94px;
    top: 116px;
    width: 202px;
    height: 80px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
  }
  .title {
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: 26px;
    line-height: 1;
    letter-spacing: 0.01em;
    white-space: nowrap;
    color: var(--text);
  }
  .dash {
    width: 40px;
    height: 1px;
    margin: 8px 0;
    background: rgba(242, 237, 228, 0.55);
  }
  .admit {
    padding-left: 0.3em;
    font-size: 10px;
    font-weight: 600;
    line-height: 12px;
    letter-spacing: 0.3em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .year {
    position: absolute;
    left: 0;
    right: 0;
    top: 16px;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 12px;
  }
  .year-rule {
    width: 64px;
    height: 1px;
    background: rgba(242, 237, 228, 0.35);
  }
  .numeral {
    padding-left: 0.3em;
    font-size: 10px;
    font-weight: 600;
    line-height: 12px;
    letter-spacing: 0.3em;
    color: var(--muted);
  }
</style>
