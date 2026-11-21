<script lang="ts">
  import Skeleton from './Skeleton.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** A heading line above everything (Requests, Downloads, Settings). */
    heading?: boolean;
    /** A tall panel, as the title pages have. */
    hero?: boolean;
    /** How many cards to show under it, in a two-column grid. */
    cards?: number;
  }

  let { heading = false, hero = false, cards = 2 }: Props = $props();
</script>

<!-- One polite announcement for assistive tech; the shapes themselves are hidden. -->
<div class="skel" role="status" aria-busy="true">
  <span class="sr-only">{t('load.loading')}</span>
  {#if heading}<Skeleton height="44px" width="220px" />{/if}
  {#if hero}<Skeleton height="290px" radius="var(--radius-lg)" />{/if}
  <div class="grid">
    {#each Array.from({ length: cards }, (_, i) => i) as i (i)}
      <Skeleton height="180px" radius="var(--radius-lg)" />
    {/each}
  </div>
</div>

<style>
  .skel {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr));
    gap: 14px;
  }
</style>
