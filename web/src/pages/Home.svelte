<script lang="ts">
  import AskSheet from '../lib/ui/AskSheet.svelte';
  import Button from '../lib/ui/Button.svelte';
  import ComingUp from '../lib/ui/ComingUp.svelte';
  import Hero from '../lib/ui/Hero.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import PosterRow from '../lib/ui/PosterRow.svelte';
  import { t } from '../lib/core/i18n';
  import { coming, featured, rows } from '../lib/mock/home';

  let askOpen = $state(false);
</script>

<Hero
  title={featured.title}
  year={featured.year}
  status={t('hero.inLibrary', { quality: featured.quality })}
  meta={featured.meta}
  blurb={featured.blurb}
  theme={featured.theme}
>
  {#snippet actions()}
    <Button variant="primary">
      <Icon name="play" size={18} filled />
      {t('hero.openIn', { server: featured.server })}
    </Button>
    <Button onclick={() => (askOpen = true)}>
      {t('hero.askFor', { quality: featured.askQuality })}
    </Button>
  {/snippet}
</Hero>

{#each rows as row (row.id)}
  <PosterRow title={row.title} href={row.href} items={row.items} />
{/each}

<ComingUp events={coming} />

<AskSheet bind:open={askOpen} title={featured.title} titleId={featured.id} />
