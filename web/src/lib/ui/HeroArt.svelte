<script lang="ts">
  import ArtPanel from './ArtPanel.svelte';
  import { skylineArt } from './art';

  interface Props {
    year: number;
  }

  let { year }: Props = $props();

  const art = $derived(skylineArt(year));
</script>

<ArtPanel {art} />
