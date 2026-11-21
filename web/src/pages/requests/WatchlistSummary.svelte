<script lang="ts">
  import { onMount } from 'svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import NotSetUp from '../../lib/ui/NotSetUp.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import { isRouteMissing } from '../../lib/core/catalog';
  import { onEvent, onResync } from '../../lib/core/events';
  import { formatAgo } from '../../lib/core/format';
  import { t } from '../../lib/core/i18n';
  import { checkWatchlists, getWatchlists, type PlexWatchlist } from '../../lib/core/serversApi';

  interface Props {
    /** Only the owner may run a check. */
    canCheck: boolean;
    /** Say something after "Check now" (a toast on the page). */
    onnote?: (message: string) => void;
  }

  let { canCheck, onnote }: Props = $props();

  const uid = $props.id();
  const headId = `watch-${uid}`;

  let plex = $state.raw<PlexWatchlist | undefined>();
  let off = $state(false);
  let failed = $state(false);
  let checking = $state(false);

  async function load(): Promise<void> {
    try {
      plex = (await getWatchlists()).plex;
      off = false;
      failed = false;
    } catch (err) {
      if (isRouteMissing(err)) off = true;
      else failed = true;
    }
  }

  onMount(() => {
    void load();
    const stop = onEvent('watchlists', () => void load());
    const stopResync = onResync(() => void load());
    return () => {
      stop();
      stopResync();
    };
  });

  const friends = $derived(plex?.friends ?? []);
  const linked = $derived(friends.filter((f) => f.linkedUserId !== null));
  const unlinked = $derived(friends.filter((f) => f.linkedUserId === null));

  const headline = $derived.by(() => {
    if (!plex || !plex.linked) return t('requests.watch.notLinked');
    const ago = formatAgo(plex.lastCheckedAt);
    return ago
      ? t('requests.watch.headline', { n: linked.length, ago })
      : t('requests.watch.headlineNever', { n: linked.length });
  });

  function list(names: string[]): string {
    return new Intl.ListFormat(undefined, { style: 'long', type: 'conjunction' }).format(names);
  }

  const detail = $derived.by(() => {
    if (!plex || !plex.linked) return t('requests.watch.linkHint');
    if (friends.length === 0) return t('requests.watch.noFriends');
    const parts: string[] = [];
    if (linked.length > 0) {
      parts.push(
        t(linked.length === 1 ? 'requests.watch.linkedOne' : 'requests.watch.linkedMany', {
          names: list(linked.map((f) => f.linkedUserName ?? f.username)),
        }),
      );
    }
    if (unlinked.length > 0) {
      parts.push(
        t(unlinked.length === 1 ? 'requests.watch.unlinkedOne' : 'requests.watch.unlinkedMany', {
          names: list(unlinked.map((f) => f.username)),
        }),
      );
    }
    return parts.join(' ');
  });

  async function check(): Promise<void> {
    if (checking) return;
    checking = true;
    try {
      await checkWatchlists();
      await load();
      onnote?.(t('requests.watch.checked'));
    } catch {
      onnote?.(t('requests.watch.checkFailed'));
    } finally {
      checking = false;
    }
  }
</script>

<Panel labelledby={headId} padding="20px">
  <h2 id={headId} class="h2">{t('requests.watchlists')}</h2>
  {#if off}
    <div class="gap"><NotSetUp hint={t('requests.watch.offHint')} /></div>
  {:else if failed && !plex}
    <p class="watch-detail" role="alert">{t('load.failed')}</p>
  {:else if plex}
    <p class="watch-head">{headline}</p>
    <p class="watch-detail">{detail}</p>
    <div class="watch-foot">
      {#if plex.linked && canCheck}
        <Button disabled={checking} onclick={check}>
          <Icon name="refresh" size={18} stroke={1.9} />
          {checking ? t('requests.watch.checking') : t('requests.checkNow')}
        </Button>
      {:else if !plex.linked && canCheck}
        <Button href="/settings?tab=connections#watchlists">{t('requests.watch.link')}</Button>
      {/if}
    </div>
  {/if}
</Panel>

<style>
  .h2 {
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .gap {
    margin-top: 8px;
  }
  .watch-head {
    margin-top: 8px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .watch-detail {
    margin-top: 4px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .watch-foot {
    margin-top: 14px;
  }
  .watch-foot:empty {
    display: none;
  }
</style>
