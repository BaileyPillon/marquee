<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  interface Props {
    href: string;
    /** Where it leads, e.g. "Movies". */
    children: Snippet;
  }

  let { href, children }: Props = $props();
</script>

<div class="wrap">
  <a class="back" {href}>
    <Icon name="back" size={18} stroke={2} />
    {@render children()}
  </a>
</div>

<style>
  .wrap {
    margin-bottom: 12px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    /* A 32px touch target that takes no more room than the 18px line. */
    padding: 7px 0;
    margin: -7px 0;
    font-size: 13.5px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
    text-decoration: none;
  }
  .back:hover {
    color: var(--text);
  }
</style>
