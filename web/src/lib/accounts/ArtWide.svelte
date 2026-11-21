<script lang="ts">
  import { t } from '../core/i18n';
  import { toRoman } from '../ui/heroArt';
  import { bulbRow, buildWideCurtain, buildWideSkyline } from './authArt';

  interface Props {
    /** The panel's flex shorthand: how much of the row it takes. */
    flex?: string;
  }

  let { flex = '1 1 520px' }: Props = $props();

  // The scene is fixed; build it once per instance.
  const curtain = buildWideCurtain();
  const skyline = buildWideSkyline();
  const footlights = bulbRow(21, 8, (i) => i);
  const bulbsTop = bulbRow(17, 12, (i) => i);
  const bulbsBottom = bulbRow(17, 12, (i) => 39 - i);
  const bulbsRight = bulbRow(6, 12, (i) => 17 + i);
  const bulbsLeft = bulbRow(6, 12, (i) => 45 - i);
  const scallops = Array.from({ length: 12 });
  const year = toRoman(new Date().getFullYear());
</script>

<div class="art" style:flex aria-hidden="true">
  <div class="ring r724"></div>
  <div class="ring r600"></div>
  <div class="ring r486"></div>
  <div class="moon"></div>

  {#each curtain as band, i (i)}
    <div style={band.style}><div style={band.foldStyle}></div></div>
  {/each}
  <div class="rule" style="left:30%"></div>
  <div class="rule" style="left:70%"></div>

  {#each skyline as style, i (i)}
    <div {style}></div>
  {/each}

  <div class="floor"></div>
  <div class="footlights">
    {#each footlights as style, i (i)}<div {style}></div>{/each}
  </div>

  <div class="sign">
    <div class="pole" style="left:18%"></div>
    <div class="pole" style="right:18%"></div>
    <div class="cap" style="left:calc(18% - 3px)"></div>
    <div class="cap" style="right:calc(18% - 3px)"></div>
    <div class="panel"></div>
    <div class="inner"></div>
    <div class="row top">
      {#each bulbsTop as style, i (i)}<div {style}></div>{/each}
    </div>
    <div class="row bottom">
      {#each bulbsBottom as style, i (i)}<div {style}></div>{/each}
    </div>
    <div class="col left">
      {#each bulbsLeft as style, i (i)}<div {style}></div>{/each}
    </div>
    <div class="col right">
      {#each bulbsRight as style, i (i)}<div {style}></div>{/each}
    </div>
    <div class="words">
      <div class="title">{t('auth.art.title')}</div>
      <div class="dash"></div>
      <div class="admit">{t('auth.art.admit')}</div>
    </div>
  </div>

  <div class="valance"></div>
  <div class="scallops">
    {#each scallops as _, i (i)}<div class="scallop"></div>{/each}
  </div>
  <div class="year">
    <div class="year-rule"></div>
    <div class="numeral">{year}</div>
    <div class="year-rule"></div>
  </div>

  <div class="frame"></div>
  <div class="stud" style="left:11px;top:11px"></div>
  <div class="stud" style="right:11px;top:11px"></div>
  <div class="stud" style="left:11px;bottom:11px"></div>
  <div class="stud" style="right:11px;bottom:11px"></div>
</div>

<style>
  .art {
    position: relative;
    isolation: isolate;
    min-width: 0;
    min-height: 480px;
    box-sizing: border-box;
    overflow: hidden;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--art-bg);
  }

  .ring {
    position: absolute;
    left: 50%;
    top: 50%;
    box-sizing: border-box;
    border-radius: 50%;
    z-index: 0;
  }
  .r724 {
    width: 724px;
    height: 724px;
    margin: -362px 0 0 -362px;
    border: 1px solid rgba(242, 237, 228, 0.1);
  }
  .r600 {
    width: 600px;
    height: 600px;
    margin: -300px 0 0 -300px;
    border: 1px solid rgba(242, 237, 228, 0.14);
  }
  .r486 {
    width: 486px;
    height: 486px;
    margin: -243px 0 0 -243px;
    border: 1px solid rgba(242, 237, 228, 0.24);
  }
  .moon {
    position: absolute;
    left: 50%;
    top: 50%;
    width: 380px;
    height: 380px;
    margin: -190px 0 0 -190px;
    border-radius: 50%;
    background: var(--art-moon);
    z-index: 0;
  }
  .rule {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 1px;
    z-index: 1;
    background: rgba(242, 237, 228, 0.35);
  }

  .floor {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 72px;
    box-sizing: border-box;
    z-index: 3;
    background: var(--art-floor);
    border-top: 1px solid rgba(242, 237, 228, 0.4);
  }
  .footlights {
    position: absolute;
    left: 36px;
    right: 36px;
    bottom: 34px;
    height: 8px;
    z-index: 3;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  /* The marquee sign. */
  .sign {
    position: relative;
    z-index: 2;
    flex: 0 0 auto;
    width: min(440px, calc(100% - 56px));
    height: 208px;
  }
  .pole {
    position: absolute;
    bottom: 100%;
    width: 1px;
    height: 1400px;
    background: rgba(242, 237, 228, 0.6);
  }
  .cap {
    position: absolute;
    top: -4px;
    width: 7px;
    height: 7px;
    background: var(--text);
  }
  .panel {
    position: absolute;
    inset: 0;
    box-sizing: border-box;
    background: var(--ground);
    box-shadow: inset 0 0 0 1px rgba(242, 237, 228, 0.45);
  }
  .inner {
    position: absolute;
    inset: 26px;
    box-sizing: border-box;
    border: 1px solid rgba(242, 237, 228, 0.2);
  }
  .row {
    position: absolute;
    left: 10px;
    right: 10px;
    height: 12px;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .row.top {
    top: 10px;
  }
  .row.bottom {
    bottom: 10px;
  }
  .col {
    position: absolute;
    top: 22px;
    bottom: 22px;
    width: 12px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: space-evenly;
  }
  .col.left {
    left: 10px;
  }
  .col.right {
    right: 10px;
  }
  .words {
    position: absolute;
    inset: 30px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
  }
  .title {
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: clamp(30px, 3.6vw, 48px);
    line-height: 1;
    letter-spacing: 0.01em;
    white-space: nowrap;
    color: var(--text);
  }
  .dash {
    width: 56px;
    height: 1px;
    margin: 10px 0;
    background: rgba(242, 237, 228, 0.55);
  }
  .admit {
    padding-left: 0.3em;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.3em;
    text-transform: uppercase;
    color: var(--text-3);
  }

  /* Valance across the top. */
  .valance {
    position: absolute;
    left: 0;
    right: 0;
    top: 0;
    height: 46px;
    box-sizing: border-box;
    z-index: 3;
    background: var(--art-valance);
    box-shadow: inset 0 -1px 0 rgba(242, 237, 228, 0.28);
  }
  .scallops {
    position: absolute;
    left: 0;
    right: 0;
    top: 46px;
    height: 26px;
    z-index: 3;
    display: flex;
  }
  .scallop {
    flex: 1 1 0;
    min-width: 0;
    height: 26px;
    box-sizing: border-box;
    background: var(--art-valance);
    border: 1px solid rgba(242, 237, 228, 0.28);
    border-top: 0;
    border-radius: 0 0 50% 50% / 0 0 100% 100%;
  }
  .year {
    position: absolute;
    left: 0;
    right: 0;
    top: 20px;
    z-index: 3;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 14px;
  }
  .year-rule {
    width: min(160px, 22%);
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

  /* Frame with corner studs. */
  .frame {
    position: absolute;
    inset: 14px;
    box-sizing: border-box;
    z-index: 4;
    border: 1px solid rgba(242, 237, 228, 0.28);
  }
  .stud {
    position: absolute;
    width: 7px;
    height: 7px;
    z-index: 4;
    background: var(--text);
  }
</style>
