<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from '../ui/Icon.svelte';

  interface Props {
    id?: string;
    children: Snippet;
  }

  let { id, children }: Props = $props();
</script>

<!-- role="alert": a screen reader reads it out as soon as it appears. -->
<div {id} class="note" role="alert">
  <Icon name="alert" size={16} stroke={2.2} />
  <span>{@render children()}</span>
</div>

<style>
  .note {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--danger);
  }
  .note :global(svg) {
    margin-top: 1px;
  }
</style>
