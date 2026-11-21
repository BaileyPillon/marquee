<script lang="ts">
  interface Props {
    /** CSS height, e.g. "290px". */
    height?: string;
    /** CSS width. Defaults to the full width of the parent. */
    width?: string;
    /** CSS border radius. */
    radius?: string;
  }

  let { height = '16px', width = '100%', radius = 'var(--radius-sm)' }: Props = $props();
</script>

<div class="skeleton" style:height style:width style:border-radius={radius} aria-hidden="true"></div>

<style>
  .skeleton {
    max-width: 100%;
    background: linear-gradient(
      90deg,
      var(--panel) 0%,
      var(--panel-hover) 50%,
      var(--panel) 100%
    );
    background-size: 200% 100%;
    box-shadow: inset 0 0 0 1px var(--hairline);
    animation: sweep 1.6s ease-in-out infinite;
  }
  @keyframes sweep {
    from {
      background-position: 100% 0;
    }
    to {
      background-position: -100% 0;
    }
  }
</style>
