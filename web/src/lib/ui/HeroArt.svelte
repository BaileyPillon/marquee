<script lang="ts">
  import { buildHeroArt, toRoman } from './heroArt';

  interface Props {
    year: number;
  }

  let { year }: Props = $props();

  // The composition is fixed; build it once for every instance.
  const shapes = buildHeroArt();
</script>

<div class="floor" aria-hidden="true"></div>
<div class="stage" aria-hidden="true">
  {#each shapes as style}
    <div {style}></div>
  {/each}
  <div class="numeral">{toRoman(year)}</div>
</div>

<style>
  /* The ground the skyline stands on runs the full width of the panel. */
  .floor {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 14px;
    background: var(--hero-tower-b);
  }
  /* A fixed 520px stage, centred when the panel is wider (hero stacked under its text). */
  .stage {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 50%;
    width: 520px;
    margin-left: -260px;
  }
  .numeral {
    position: absolute;
    left: 0;
    right: 0;
    top: 20px;
    text-align: center;
    font-size: 10px;
    font-weight: 600;
    line-height: 12px;
    letter-spacing: 0.3em;
    color: var(--muted);
  }
</style>
