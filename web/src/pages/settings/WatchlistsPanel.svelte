<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import ConfirmDialog from '../../lib/ui/ConfirmDialog.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import NotSetUp from '../../lib/ui/NotSetUp.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import StatusDot from '../../lib/ui/StatusDot.svelte';
  import Toggle from '../../lib/ui/Toggle.svelte';
  import { failureText, isRouteMissing } from '../../lib/core/catalog';
  import { onEvent, onResync } from '../../lib/core/events';
  import { formatAgo } from '../../lib/core/format';
  import { t } from '../../lib/core/i18n';
  import { peopleState } from '../../lib/core/people.svelte';
  import {
    checkWatchlists,
    getWatchlists,
    linkPlexFriend,
    pollPlexLink,
    savePlexSettings,
    startPlexLink,
    unlinkPlex,
    type PlexLinkStart,
    type PlexSettings,
    type PlexWatchlist,
  } from '../../lib/core/serversApi';

  interface Props {
    onnote?: (message: string) => void;
  }

  let { onnote }: Props = $props();

  const uid = $props.id();
  const headId = `watchlists-${uid}`;

  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let status = $state<Status>('loading');
  let plex = $state.raw<PlexWatchlist | undefined>();

  async function load(): Promise<void> {
    try {
      plex = (await getWatchlists()).plex;
      status = 'ready';
    } catch (err) {
      if (isRouteMissing(err)) status = 'off';
      else if (status !== 'ready') status = 'failed';
    }
  }

  onMount(() => {
    void load();
    void peopleState.reload();
    const stop = onEvent('watchlists', () => void load());
    const stopResync = onResync(() => void load());
    return () => {
      stop();
      stopResync();
    };
  });

  // ---- linking: Plex shows a code, the owner approves it on plex.tv, and we poll until it is done ----
  const POLL_MS = 2000;
  const LINK_WINDOW_MS = 5 * 60_000;
  let link = $state.raw<PlexLinkStart | undefined>();
  let linkBusy = $state(false);
  let linkError = $state('');
  let pollTimer: number | undefined;
  let deadline = 0;

  function stopPolling(): void {
    window.clearInterval(pollTimer);
    pollTimer = undefined;
  }

  async function poll(): Promise<void> {
    const current = link;
    if (!current) return;
    if (Date.now() > deadline) {
      stopPolling();
      link = undefined;
      linkError = t('watch.link.expired');
      return;
    }
    try {
      const res = await pollPlexLink(current.pinId);
      if (res.linked === true && link === current) {
        stopPolling();
        link = undefined;
        await load();
        void peopleState.reload();
        onnote?.(t('watch.link.done'));
      } else if (res.error && link === current) {
        stopPolling();
        link = undefined;
        linkError = res.error;
      }
    } catch {
      // A missed answer: the next tick asks again.
    }
  }

  async function startLink(): Promise<void> {
    if (linkBusy) return;
    linkBusy = true;
    linkError = '';
    try {
      link = await startPlexLink();
      deadline = Date.now() + LINK_WINDOW_MS;
      // Open Plex in a new tab. A browser may block it; the button below does the same.
      window.open(link.authUrl, '_blank', 'noopener,noreferrer');
      stopPolling();
      pollTimer = window.setInterval(() => void poll(), POLL_MS);
    } catch (err) {
      linkError = failureText(err);
    } finally {
      linkBusy = false;
    }
  }

  function cancelLink(): void {
    stopPolling();
    link = undefined;
  }

  onDestroy(stopPolling);

  // ---- unlink (asks first) ----
  let unlinkOpen = $state(false);
  let unlinkBusy = $state(false);
  let unlinkError = $state('');

  async function confirmUnlink(): Promise<void> {
    if (unlinkBusy) return;
    unlinkBusy = true;
    unlinkError = '';
    try {
      await unlinkPlex();
      unlinkOpen = false;
      await load();
      onnote?.(t('watch.unlinked'));
    } catch (err) {
      unlinkError = failureText(err);
    } finally {
      unlinkBusy = false;
    }
  }

  // ---- friends ----
  let savingFriend = $state<string | null>(null);
  let friendError = $state('');

  async function chooseMember(plexUserId: string, value: string): Promise<void> {
    if (savingFriend) return;
    savingFriend = plexUserId;
    friendError = '';
    try {
      await linkPlexFriend(plexUserId, value === '' ? null : Number(value));
      await load();
    } catch (err) {
      friendError = failureText(err);
      await load();
    } finally {
      savingFriend = null;
    }
  }

  // ---- options: whether watchlists are read, whose, who is made for a new friend, and the quality ----
  let savingOption = $state(false);
  let optionError = $state('');
  let quality = $state<string>('hd');

  // The select shows what the server has; a save (or a reload) puts it back in step.
  $effect(() => {
    quality = plex?.quality ?? 'hd';
  });

  async function setOption(change: PlexSettings): Promise<void> {
    if (savingOption || !plex) return;
    savingOption = true;
    optionError = '';
    try {
      plex = await savePlexSettings(change);
      onnote?.(t('watch.options.saved'));
    } catch (err) {
      optionError = failureText(err);
      await load(); // show what the server really has
    } finally {
      savingOption = false;
    }
  }

  // ---- check now ----
  let checking = $state(false);

  async function check(): Promise<void> {
    if (checking) return;
    checking = true;
    try {
      await checkWatchlists();
      await load();
      onnote?.(t('requests.watch.checked'));
    } catch (err) {
      onnote?.(failureText(err));
    } finally {
      checking = false;
    }
  }

  const friends = $derived(plex?.friends ?? []);
  const members = $derived(peopleState.people.filter((p) => p.role === 'member'));

  const lastCheck = $derived.by(() => {
    if (!plex?.lastCheckedAt) return t('watch.neverChecked');
    const asked = plex.lastResult?.asked ?? 0;
    const skipped = Object.values(plex.lastResult?.skipped ?? {}).reduce((a, b) => a + b, 0);
    return t('watch.lastCheck', {
      ago: formatAgo(plex.lastCheckedAt),
      asked,
      skipped,
    });
  });
</script>

{#snippet options()}
  <h3 class="friends-head">{t('watch.options.heading')}</h3>
  <div class="options">
    <Toggle
      checked={plex?.enabled ?? false}
      label={t('watch.options.enabled')}
      onchange={(on) => void setOption({ enabled: on })}
    />
    <Toggle
      checked={plex?.includeFriends ?? false}
      label={t('watch.options.friends')}
      onchange={(on) => void setOption({ includeFriends: on })}
    />
    <Toggle
      checked={plex?.autoCreateMembers ?? false}
      label={t('watch.options.autoCreate')}
      onchange={(on) => void setOption({ autoCreateMembers: on })}
    />
  </div>
  <div class="quality">
    <SelectField
      label={t('watch.options.quality')}
      bind:value={quality}
      options={[
        { value: 'hd', label: t('ask.quality.hd.short') },
        { value: 'uhd', label: t('ask.quality.uhd.short') },
      ]}
      help={t('watch.options.qualityHelp')}
      onchange={() => void setOption({ quality: quality === 'uhd' ? 'uhd' : 'hd' })}
    />
  </div>
  {#if optionError}<p class="problem" role="alert">{optionError}</p>{/if}
{/snippet}

<Panel labelledby={headId} padding="18px">
  <div id="watchlists">
    <SectionHead title={t('watch.heading')} id={headId} size="md" align="center" gap={4} minHeight={36}>
      {#if plex?.linked}
        <Button size="sm" disabled={checking} onclick={check}>
          <Icon name="refresh" size={16} stroke={2.2} />
          {checking ? t('requests.watch.checking') : t('requests.checkNow')}
        </Button>
      {/if}
    </SectionHead>
  </div>

  {#if status === 'off'}
    <NotSetUp hint={t('requests.watch.offHint')} />
  {:else if status === 'failed'}
    <LoadFailed onretry={() => void load()} />
  {:else if status === 'loading' || !plex}
    <div class="loading" role="status" aria-busy="true">
      <span class="sr-only">{t('load.loading')}</span>
      <Skeleton height="64px" radius="var(--radius-md)" />
    </div>
  {:else if !plex.linked}
    <p class="help">{t('watch.help')}</p>
    {@render options()}
    {#if link}
      <div class="pin" role="status">
        <p class="pin-title">{t('watch.link.waiting')}</p>
        <p class="pin-code">
          <span class="pin-label">{t('watch.link.code')}</span>
          <code>{link.code}</code>
        </p>
        <p class="help">{t('watch.link.help')}</p>
        <div class="row">
          <Button variant="primary" size="sm" external href={link.authUrl}>
            {t('watch.link.open')}
          </Button>
          <Button size="sm" onclick={cancelLink}>{t('confirm.cancel')}</Button>
        </div>
      </div>
    {:else}
      <div class="row">
        <Button variant="primary" disabled={linkBusy} onclick={startLink}>
          {linkBusy ? t('watch.link.starting') : t('requests.watch.link')}
        </Button>
      </div>
    {/if}
    {#if linkError}<p class="problem" role="alert">{linkError}</p>{/if}
  {:else}
    <div class="who">
      <StatusDot kind="solid">{t('watch.linkedAs', { user: plex.user ?? '' })}</StatusDot>
      {#if plex.enabled}
        <Chip tone="solid" size="micro">{t('watch.on')}</Chip>
      {:else}
        <Chip tone="dashed" size="micro">{t('watch.paused')}</Chip>
      {/if}
    </div>
    <p class="help">
      {plex.includeFriends ? t('watch.friendsIncluded') : t('watch.friendsNotIncluded')}
    </p>
    <p class="last" role="status">{lastCheck}</p>
    {#if plex.lastError}<p class="problem">{t('watch.lastError', { why: plex.lastError })}</p>{/if}
    {@render options()}

    {#if plex.includeFriends}
      <h3 class="friends-head">{t('watch.friends')}</h3>
      {#if friends.length === 0}
        <p class="help">{t('requests.watch.noFriends')}</p>
      {:else}
        <ul class="friends">
          {#each friends as friend (friend.plexUserId)}
            {@const selectId = `friend-${uid}-${friend.plexUserId}`}
            <li>
              <span class="friend-name">{friend.username}</span>
              <label class="sr-only" for={selectId}>{t('watch.memberFor', { name: friend.username })}</label>
              <select
                id={selectId}
                value={friend.linkedUserId === null ? '' : String(friend.linkedUserId)}
                disabled={savingFriend === friend.plexUserId}
                onchange={(e) => void chooseMember(friend.plexUserId, e.currentTarget.value)}
              >
                <option value="">{t('watch.noMember')}</option>
                {#each members as member (member.id)}
                  <option value={String(member.id)}>{member.name}</option>
                {/each}
                {#if friend.linkedUserId !== null && !members.some((m) => m.id === friend.linkedUserId)}
                  <option value={String(friend.linkedUserId)}>{friend.linkedUserName ?? friend.linkedUserId}</option>
                {/if}
              </select>
            </li>
          {/each}
        </ul>
      {/if}
      {#if friendError}<p class="problem" role="alert">{friendError}</p>{/if}
    {/if}

    <div class="row foot">
      <Button
        size="sm"
        onclick={() => {
          unlinkError = '';
          unlinkOpen = true;
        }}
      >
        {t('watch.unlink')}
      </Button>
    </div>
  {/if}
</Panel>

<ConfirmDialog
  bind:open={unlinkOpen}
  title={t('watch.unlink.title')}
  body={t('watch.unlink.body')}
  confirmLabel={t('watch.unlink')}
  busy={unlinkBusy}
  error={unlinkError}
  onconfirm={confirmUnlink}
/>

<style>
  .loading {
    margin-top: 8px;
  }
  .help {
    margin: 8px 0 0;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
  }
  .foot {
    padding-top: 14px;
    border-top: 1px solid var(--hairline-strong);
  }
  .problem {
    margin-top: 10px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--danger);
  }
  .pin {
    margin-top: 14px;
    padding: 14px;
    border-radius: var(--radius-md);
    background: var(--rail);
    box-shadow: inset 0 0 0 1px var(--hairline);
  }
  .pin-title {
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .pin-code {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 4px 10px;
    margin-top: 8px;
  }
  .pin-label {
    font-size: 13px;
    color: var(--text-3);
  }
  .pin-code code {
    font-family: var(--font-mono);
    font-size: 22px;
    font-weight: 600;
    letter-spacing: 0.14em;
    color: var(--text);
  }
  .who {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    margin-top: 6px;
    color: var(--text-2);
  }
  .last {
    margin-top: 8px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .options {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 8px;
  }
  .quality {
    max-width: 260px;
    margin-top: 12px;
  }
  .friends-head {
    margin-top: 16px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .friends {
    margin-top: 6px;
  }
  .friends li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px 12px;
    padding: 10px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .friend-name {
    flex: 1 1 140px;
    min-width: 0;
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  select {
    flex: 1 1 180px;
    box-sizing: border-box;
    max-width: 260px;
    height: 40px;
    padding: 0 10px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 14px;
    color-scheme: dark;
  }
  select:focus-visible {
    border-color: var(--accent);
  }
</style>
