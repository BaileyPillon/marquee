<script lang="ts">
  import PosterCard from './PosterCard.svelte';
  import { titleHref } from '../core/posters';
  import { t } from '../core/i18n';
  import type { PosterItem } from '../core/types';

  interface Props {
    title: string;
    /** Where "See all" goes. */
    href: string;
    items: PosterItem[];
  }

  let { title, href, items }: Props = $props();

  const uid = $props.id();
  const headingId = `row-${uid}`;
</script>

<section class="row" aria-labelledby={headingId}>
  <div class="head">
    <h2 id={headingId}>{title}</h2>
    <a class="all" {href}>{t('action.seeAll')}</a>
  </div>
  <ul class="scroller">
    {#each items as item (item.id)}
      <li>
        <PosterCard
          href={titleHref(item)}
          title={item.title}
          year={item.year}
          status={item.status}
          caption={item.caption}
          tone={item.tone}
        />
      </li>
    {/each}
  </ul>
</section>

<style>
  .row {
    margin-bottom: 18px;
  }
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
  }
  h2 {
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .all {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
    text-decoration: none;
  }
  .all:hover {
    color: var(--text);
  }
  .scroller {
    display: flex;
    gap: 12px;
    overflow-x: auto;
    padding-bottom: 4px;
    scrollbar-width: thin;
    scrollbar-color: var(--input-border) transparent;
  }
  li {
    display: block;
  }

  /* Phone: rows run edge to edge and the "See all" link gets a 44px touch target. */
  @media (max-width: 899.98px) {
    .head {
      align-items: center;
      min-height: 44px;
      margin-bottom: 0;
    }
    .all {
      display: inline-flex;
      align-items: center;
      min-height: 44px;
      padding-left: 12px;
    }
    .scroller {
      margin: 0 -16px;
      padding: 0 16px 4px;
      scrollbar-width: none;
    }
  }
</style>
