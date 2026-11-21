<script lang="ts">
  import BackLink from './BackLink.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Panel from './Panel.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** The library grid this title would have been in. */
    backHref: string;
    backLabel: string;
  }

  let { backHref, backLabel }: Props = $props();
</script>

<BackLink href={backHref}>{backLabel}</BackLink>
<Panel tag="div" padding="22px">
  <h1>{t('titleNotFound.heading')}</h1>
  <p>{t('titleNotFound.body')}</p>
  <div class="actions">
    <Button variant="primary" href="/explore">
      <Icon name="explore" size={18} />
      {t('titleNotFound.explore')}
    </Button>
    <Button href={backHref}>{backLabel}</Button>
  </div>
</Panel>

<style>
  h1 {
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: 30px;
    line-height: 1.15;
    color: var(--text);
  }
  p {
    margin-top: 8px;
    font-size: 14.5px;
    line-height: 21px;
    color: var(--text-3);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 18px;
  }
</style>
