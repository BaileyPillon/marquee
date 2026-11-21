<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import { t } from '../core/i18n';
  import { resource } from '../core/resource.svelte.ts';
  import { getOpenTarget } from '../core/serversApi';

  interface Props {
    titleId: number;
  }

  let { titleId }: Props = $props();

  // "Open in Jellyfin" names the media server that has the title. A title that is on none (or a
  // server that cannot say) answers 404, and then there is no button at all.
  const target = resource(
    () => String(titleId),
    async (key, signal) => {
      try {
        return await getOpenTarget(Number(key), signal);
      } catch (err) {
        if (signal.aborted) throw err;
        return null;
      }
    },
  );
  const open = $derived(target.data);
</script>

{#if open}
  <Button
    external
    href={open.url}
    aria-label={t('toolbar.openInAria', { server: open.server.name })}
  >
    <Icon name="playOutline" size={18} stroke={2} />
    {t('hero.openIn', { server: open.server.name })}
  </Button>
{/if}
