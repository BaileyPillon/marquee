<script lang="ts">
  import type { ArtComposition, ArtShape } from './art';

  interface Props {
    /** The shapes and small capitals to draw: skylineArt(), horizonArt() or keysArt(). */
    art: ArtComposition;
  }

  let { art }: Props = $props();

  /** Runs of shapes that share a layer, in paint order, so a full-width shape can sit between two stage shapes. */
  const layers = $derived.by(() => {
    const out: Array<{ bleed: boolean; shapes: ArtShape[] }> = [];
    for (const shape of art.shapes) {
      const bleed = shape.bleed === true;
      const last = out[out.length - 1];
      if (last && last.bleed === bleed) last.shapes.push(shape);
      else out.push({ bleed, shapes: [shape] });
    }
    return out;
  });
</script>

<!-- Decorative: the page around it carries the meaning. -->
<div class="art" aria-hidden="true">
  {#each layers as layer, i (i)}
    <div class={layer.bleed ? 'bleed' : 'stage'}>
      {#each layer.shapes as shape, j (j)}
        <div style={shape.css}></div>
      {/each}
    </div>
  {/each}
  <div class="stage">
    {#if art.numeral}
      <div class="caps numeral {art.numeral.tone}">{art.numeral.text}</div>
    {/if}
    {#each art.labels ?? [] as label (label.text)}
      <div class="caps label" style:left="{label.left}px" style:top="{label.top}px">{label.text}</div>
    {/each}
  </div>
</div>

<style>
  .art {
    position: absolute;
    inset: 0;
  }
  /* Spans the whole panel: the sky, the ground. */
  .bleed {
    position: absolute;
    inset: 0;
  }
  /* A fixed 520px stage, centred when the panel is wider (art stacked under its text). */
  .stage {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 50%;
    width: 520px;
    margin-left: -260px;
  }
  .caps {
    position: absolute;
    font-size: 10px;
    font-weight: 600;
    line-height: 12px;
    letter-spacing: 0.3em;
    white-space: nowrap;
  }
  .numeral {
    left: 0;
    right: 0;
    top: 20px;
    text-align: center;
  }
  .muted {
    color: var(--muted);
  }
  .soft {
    color: var(--text-2);
  }
  .label {
    color: var(--muted);
  }
</style>
