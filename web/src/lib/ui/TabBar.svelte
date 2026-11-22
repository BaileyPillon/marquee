<script lang="ts">
  import Icon from './Icon.svelte';
  import { t } from '../core/i18n';
  import type { Section } from '../core/router';
  import { tabItemsFor } from './nav';

  interface Props {
    active: Section | null;
    /** Who is signed in: the tabs differ for an owner or admin and for a member. */
    role?: 'owner' | 'admin' | 'member' | null;
    /** Count shown on a tab, by section. */
    badges?: Partial<Record<Section, number>>;
  }

  let { active, role = null, badges = {} }: Props = $props();

  const items = $derived(tabItemsFor(role));
</script>

<nav class="tabbar" aria-label={t('nav.main')}>
  {#each items as tab (tab.section)}
    {@const current = tab.section === active}
    {@const count = badges[tab.section]}
    <a class="tab" class:current href={tab.href} aria-current={current ? 'page' : undefined}>
      <span class="pill">
        <Icon name={tab.icon} size={24} stroke={tab.iconStroke ?? 1.75} />
        {#if count}<span class="count">{count}</span>{/if}
      </span>
      <span class="label">{t(tab.label)}</span>
    </a>
  {/each}
</nav>

<style>
  .tabbar {
    position: fixed;
    inset-inline: 0;
    bottom: 0;
    z-index: 20;
    display: flex;
    box-sizing: border-box;
    min-height: var(--tabbar-height);
    /* Narrow side padding: five labels share the width, and a six-letter Japanese word needs 72px. */
    padding: 6px 2px calc(10px + env(safe-area-inset-bottom));
    background: var(--rail);
    border-top: 1px solid var(--hairline);
  }
  .tab {
    flex: 1 1 0;
    min-width: 0;
    min-height: 56px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 3px;
    text-decoration: none;
    color: var(--muted);
  }
  .tab:hover {
    color: var(--muted);
  }
  .pill {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 56px;
    height: 30px;
    border-radius: 15px;
  }
  .label {
    max-width: 100%;
    text-align: center;
    font-size: 12px;
    font-weight: 500;
    line-height: 14px;
    color: var(--muted);
  }
  .current .pill {
    background: var(--tab-active);
    color: var(--accent);
  }
  .current .label {
    font-weight: 700;
    color: var(--accent);
  }
  .count {
    position: absolute;
    top: -4px;
    inset-inline-end: 6px;
    box-sizing: border-box;
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    border-radius: 9px;
    background: var(--text);
    color: var(--ground);
    text-align: center;
    font-size: 11px;
    font-weight: 700;
    line-height: 18px;
  }
</style>
