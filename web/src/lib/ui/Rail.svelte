<script lang="ts">
  import Badge from './Badge.svelte';
  import Icon from './Icon.svelte';
  import OfflineNote from './OfflineNote.svelte';
  import Wordmark from './Wordmark.svelte';
  import { t } from '../core/i18n';
  import type { Section } from '../core/router';
  import { navGroups, settingsItem, type NavItem } from './nav';

  interface Props {
    /** The rail item to mark with aria-current. */
    active: Section | null;
    /** Count shown on an item, by section (Requests: asks waiting for the owner). */
    badges?: Partial<Record<Section, number>>;
    owner: { name: string; initial: string; role: 'owner' | 'admin' | 'member' };
    offline?: boolean;
  }

  let { active, badges = {}, owner, offline = false }: Props = $props();

  const uid = $props.id();
</script>

{#snippet item(nav: NavItem)}
  {@const current = nav.section === active}
  {@const count = badges[nav.section]}
  <a class="item" class:current href={nav.href} aria-current={current ? 'page' : undefined}>
    <Icon name={nav.icon} size={20} />
    <span class="label">{t(nav.label)}</span>
    {#if count}<Badge tone="count">{count}</Badge>{/if}
  </a>
{/snippet}

<aside class="rail">
  <Wordmark />
  <nav class="main" aria-label={t('nav.main')}>
    {#each navGroups as group (group.id)}
      {#if group.label}
        {@const labelId = `${uid}-${group.id}`}
        <div class="group" role="group" aria-labelledby={labelId}>
          <div class="group-label" id={labelId}>{t(group.label)}</div>
          {#each group.items as nav (nav.section)}{@render item(nav)}{/each}
        </div>
      {:else}
        <div class="group">
          {#each group.items as nav (nav.section)}{@render item(nav)}{/each}
        </div>
      {/if}
    {/each}
  </nav>

  <div class="foot">
    <div class="note"><OfflineNote {offline} /></div>
    <nav class="settings" aria-label={t('nav.settingsLabel')}>{@render item(settingsItem)}</nav>
    <div class="owner-block">
      <a class="owner" href="/settings">
        <span class="avatar" aria-hidden="true">{owner.initial}</span>
        <span class="who">{owner.name}<span class="role">{` · ${t(`role.${owner.role}`)}`}</span></span>
      </a>
    </div>
  </div>
</aside>

<style>
  .rail {
    position: sticky;
    top: 0;
    flex: 0 0 var(--rail-width);
    width: var(--rail-width);
    height: 100vh;
    height: 100dvh;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    padding: 22px 14px 18px;
    overflow-y: auto;
    background: var(--rail);
    border-right: 1px solid var(--hairline);
  }
  .main {
    display: flex;
    flex-direction: column;
    gap: 20px;
    margin-bottom: 24px;
  }
  .group {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .group-label {
    padding: 0 12px 6px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .item {
    display: flex;
    align-items: center;
    gap: 12px;
    box-sizing: border-box;
    min-height: 42px;
    padding: 0 12px;
    border-radius: var(--radius-sm);
    color: var(--text-2);
    font-size: 14.5px;
    font-weight: 500;
    line-height: 1;
    text-decoration: none;
  }
  .item:hover {
    background: var(--hover);
    color: var(--text);
  }
  .item.current {
    background: var(--nav-active);
    color: var(--accent);
    font-weight: 600;
  }
  .label {
    flex: 1 1 auto;
  }
  .foot {
    margin-top: auto;
    display: flex;
    flex-direction: column;
  }
  .note {
    padding: 0 12px;
  }
  .note :global(.pill) {
    margin-bottom: 10px;
  }
  .settings {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-bottom: 12px;
  }
  .owner-block {
    padding-top: 14px;
    border-top: 1px solid var(--hairline);
  }
  .owner {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 10px;
    border-radius: var(--radius-sm);
    color: var(--text);
    text-decoration: none;
  }
  .owner:hover {
    background: var(--hover);
    color: var(--text);
  }
  .avatar {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: var(--avatar);
    font-size: 14px;
    font-weight: 600;
    line-height: 1;
    color: var(--text);
  }
  .who {
    font-size: 14px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text);
  }
  .role {
    color: var(--muted);
  }
</style>
