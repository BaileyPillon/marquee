<script lang="ts">
  interface Props {
    /** 0 to 100. */
    value: number;
    /**
     * A name makes it a progress bar for assistive tech. Leave it out when the percentage is
     * already written next to the bar.
     */
    label?: string;
    /** Bar height in px. */
    height?: number;
    /** text: light fill. accent: amber fill (importing). */
    tone?: 'text' | 'accent';
    /** Round the track and the fill (downloads). */
    rounded?: boolean;
  }

  let { value, label, height = 6, tone = 'text', rounded = false }: Props = $props();

  const pct = $derived(Math.min(100, Math.max(0, value)));
</script>

<div
  class="track"
  class:rounded
  style:height="{height}px"
  role={label ? 'progressbar' : undefined}
  aria-label={label}
  aria-valuemin={label ? 0 : undefined}
  aria-valuemax={label ? 100 : undefined}
  aria-valuenow={label ? pct : undefined}
  aria-hidden={label ? undefined : 'true'}
>
  <div class="fill {tone}" class:rounded style:width="{pct}%"></div>
</div>

<style>
  .track {
    overflow: hidden;
    background: var(--track-faint);
  }
  .fill {
    height: 100%;
  }
  .text {
    background: var(--text);
  }
  .accent {
    background: var(--accent);
  }
  .rounded {
    border-radius: 3px;
  }
</style>
