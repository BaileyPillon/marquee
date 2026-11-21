<script lang="ts">
  import { artworkUrl } from '../core/catalog';

  interface Props {
    /** An artwork ref (`tmdb/w500/abc.jpg`): the image is loaded from `/api/v1/artwork/<ref>`. */
    imageRef: string;
    /** True once the picture is showing: a caller can lay a scrim over it only then. */
    loaded?: boolean;
  }

  let { imageRef, loaded = $bindable(false) }: Props = $props();

  // The picture lies over whatever sits beneath it (a typographic poster, a drawn panel). It is
  // invisible until it has loaded and it removes itself when it cannot load, so the poster
  // underneath is always the fallback.
  let failed = $state(false);
  let img: HTMLImageElement | undefined = $state();

  $effect(() => {
    void imageRef;
    loaded = false;
    failed = false;
  });

  // A cached picture can finish before the load handler is attached.
  $effect(() => {
    if (img && img.complete && img.naturalWidth > 0) loaded = true;
  });
</script>

{#if !failed}
  <img
    bind:this={img}
    class="art"
    class:loaded
    src={artworkUrl(imageRef)}
    alt=""
    loading="lazy"
    decoding="async"
    onload={() => (loaded = true)}
    onerror={() => (failed = true)}
  />
{/if}

<style>
  .art {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    opacity: 0;
    transition: opacity 0.3s ease;
  }
  .loaded {
    opacity: 1;
  }
</style>
