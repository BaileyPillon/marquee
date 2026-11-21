<script lang="ts">
  import { posterColor } from '../core/posters';

  interface Props {
    /** The title it stands for: its first letter is drawn on its poster colour. */
    title: string;
    /** Size in px. */
    width: number;
    height: number;
    /** Letter size in px. */
    fontSize: number;
    radius?: number;
  }

  let { title, width, height, fontSize, radius = 5 }: Props = $props();
</script>

<!-- A poster reduced to a coloured chip with an initial: the title is always written beside it. -->
<div
  class="thumb"
  aria-hidden="true"
  style:width="{width}px"
  style:height="{height}px"
  style:font-size="{fontSize}px"
  style:border-radius="{radius}px"
  style:background={posterColor(title)}
>
  {title.charAt(0)}
</div>

<style>
  .thumb {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: inset 0 0 0 1px var(--poster-edge);
    font-family: var(--font-serif);
    line-height: 1;
    color: var(--text);
  }
</style>
