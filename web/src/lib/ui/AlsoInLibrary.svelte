<script lang="ts">
  import PosterRow from './PosterRow.svelte';
  import { listPath, listTitles, type ApiKind, type Title } from '../core/catalog';
  import { posterItem } from '../core/posterItems';

  interface Props {
    kind: ApiKind;
    /** The title whose page this is: left out of the row. */
    exceptId: number;
    /** The row's heading, e.g. "More movies in your library". */
    heading: string;
  }

  let { kind, exceptId, heading }: Props = $props();

  let titles = $state.raw<Title[]>([]);

  // The newest others of the same kind: the catalog has no "similar" yet, so this is what is nearby.
  $effect(() => {
    const k = kind;
    const controller = new AbortController();
    listTitles({ kind: k, sort: 'added', limit: 13 }, controller.signal).then(
      (res) => (titles = res.items),
      () => {
        // The row is an extra: it simply stays away when it cannot load.
      },
    );
    return () => controller.abort();
  });

  const items = $derived(titles.filter((t) => t.id !== exceptId).slice(0, 12).map(posterItem));
</script>

{#if items.length > 0}
  <PosterRow title={heading} href={listPath(kind)} {items} />
{/if}
