<script lang="ts">
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import { t } from '../lib/core/i18n';
  import type { Match } from '../lib/core/router';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const id = $derived(match.params['id']);
</script>

<PageHeader title={t(match.route.title)} subtitle={id ? t('stub.id', { id }) : t('stub.soon')} />
{#if id}<p class="soon">{t('stub.soon')}</p>{/if}

<style>
  .soon {
    font-size: 14px;
    line-height: 20px;
    color: var(--muted);
  }
</style>
