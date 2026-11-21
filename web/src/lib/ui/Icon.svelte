<script lang="ts">
  import { icons, type IconName } from './icons';

  interface Props {
    name: IconName;
    size?: number;
    stroke?: number;
    /** Solid glyph (the play triangle) instead of a stroked one. */
    filled?: boolean;
  }

  let { name, size = 20, stroke = 1.75, filled = false }: Props = $props();
</script>

<svg
  viewBox="0 0 24 24"
  width={size}
  height={size}
  fill={filled ? 'currentColor' : 'none'}
  stroke={filled ? 'none' : 'currentColor'}
  stroke-width={stroke}
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
  focusable="false"
>
  <path d={icons[name]} />
</svg>

<style>
  svg {
    flex: 0 0 auto;
  }
</style>
