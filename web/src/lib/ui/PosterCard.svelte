<script lang="ts">
  import Poster from './Poster.svelte';
  import type { PosterSize } from '../core/posters';
  import type { CaptionTone, Status } from '../core/types';

  interface Props {
    href: string;
    title: string;
    year: string;
    status?: Status;
    caption: string;
    tone?: CaptionTone;
    size?: PosterSize;
    /** The real artwork, over the typographic poster. */
    imageRef?: string;
  }

  let { href, title, year, status, caption, tone = 'quiet', size = 'md', imageRef }: Props = $props();
</script>

<a class="card {size}" {href}>
  <Poster {title} {year} {status} {size} {imageRef} />
  <div class="caption {tone}">{caption}</div>
</a>

<style>
  .card {
    display: block;
    flex: 0 0 150px;
    width: 150px;
    color: inherit;
    text-decoration: none;
  }
  .card:hover {
    color: inherit;
  }
  .sm {
    flex-basis: 108px;
    width: 108px;
  }
  .mini {
    flex-basis: 96px;
    width: 96px;
  }
  .caption {
    margin-top: 8px;
    min-height: 16px;
    font-size: 12.5px;
    font-weight: 500;
    line-height: 16px;
    color: var(--muted);
  }
  .asked {
    min-height: 32px;
    color: var(--text-ask);
  }
  .waiting {
    min-height: 32px;
    color: var(--accent);
    font-weight: 600;
  }
</style>
