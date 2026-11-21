<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Poster from './Poster.svelte';
  import { titlePath, type SearchHit } from '../core/catalog';
  import { kindLabel } from '../core/format';
  import { t } from '../core/i18n';
  import type { Status } from '../core/types';

  interface Props {
    hit: SearchHit;
    /** add: an owner or admin saves it to the catalog. ask: a member asks for it. */
    mode: 'add' | 'ask';
    /** The add is under way. */
    busy?: boolean;
    onadd: (hit: SearchHit) => void;
    onask: (hit: SearchHit) => void;
  }

  let { hit, mode, busy = false, onadd, onask }: Props = $props();

  const library: Status = { kind: 'library' };
  const year = $derived(hit.year ? String(hit.year) : '');
  const sub = $derived([kindLabel(hit.kind), year, hit.detail].filter(Boolean).join(' · '));
  const uid = $props.id();
  const nameId = `result-${uid}`;
</script>

<article class="card" aria-labelledby={nameId}>
  <Poster
    title={hit.name}
    year={year}
    size="sm"
    status={hit.inLibrary ? library : undefined}
    imageRef={hit.poster?.ref || undefined}
  />
  <div class="info">
    <h3 id={nameId}>{hit.name}</h3>
    <p class="sub">{sub}</p>
    {#if hit.overview}<p class="overview">{hit.overview}</p>{/if}
    <div class="action">
      {#if hit.inLibrary && hit.titleId}
        <Button size="sm" href={titlePath(hit.kind, hit.titleId)} aria-label={t('explore.openAria', { name: hit.name })}>
          {t('explore.open')}
        </Button>
      {:else if mode === 'add'}
        <Button
          variant="primary"
          size="sm"
          disabled={busy}
          aria-label={t('explore.addAria', { name: hit.name })}
          onclick={() => onadd(hit)}
        >
          <Icon name="plus" size={16} stroke={2.2} />
          {busy ? t('explore.adding') : t('action.add')}
        </Button>
      {:else}
        <Button
          variant="primary"
          size="sm"
          aria-label={t('explore.askAria', { name: hit.name })}
          onclick={() => onask(hit)}
        >
          {t('explore.ask')}
        </Button>
      {/if}
    </div>
  </div>
</article>

<style>
  .card {
    display: flex;
    gap: 14px;
    min-width: 0;
    padding: 12px;
    border-radius: var(--radius-lg);
    background: var(--panel);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
  }
  .card > :global(.poster) {
    flex: 0 0 auto;
  }
  .info {
    flex: 1 1 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  h3 {
    font-size: 16px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .sub {
    margin-top: 1px;
    font-size: 12.5px;
    font-weight: 500;
    line-height: 17px;
    color: var(--text-3);
  }
  .overview {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    margin-top: 6px;
    overflow: hidden;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-2);
  }
  .action {
    margin-top: auto;
    padding-top: 10px;
  }
</style>
