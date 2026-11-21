<script lang="ts">
  import Icon from './Icon.svelte';
  import OfflineNote from './OfflineNote.svelte';
  import SearchField from './SearchField.svelte';
  import Wordmark from './Wordmark.svelte';
  import { t } from '../core/i18n';

  interface Props {
    owner: { name: string; initial: string };
    offline?: boolean;
  }

  let { owner, offline = false }: Props = $props();

  const uid = $props.id();
  const searchId = `phone-search-${uid}`;
  let open = $state(false);
  let toggle: HTMLButtonElement | undefined = $state();

  function close(): void {
    open = false;
  }

  function onkeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) {
      close();
      toggle?.focus();
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="top">
  <header class="bar">
    <Wordmark size="phone" />
    <div class="actions">
      <button
        type="button"
        class="icon-btn"
        bind:this={toggle}
        aria-label={open ? t('search.close') : t('search.open')}
        aria-expanded={open}
        aria-controls={searchId}
        onclick={() => (open = !open)}
      >
        <Icon name={open ? 'close' : 'search'} size={22} />
      </button>
      <a class="icon-btn" href="/settings" aria-label={t('a11y.profile', { name: owner.name })}>
        <span class="avatar" aria-hidden="true">{owner.initial}</span>
      </a>
    </div>
  </header>
  <div id={searchId} class="search-row" hidden={!open}>
    {#if open}
      <SearchField focusOnMount onsubmitted={close} />
    {/if}
  </div>
  <div class="note" class:on={offline}><OfflineNote {offline} /></div>
</div>

<style>
  .top {
    position: sticky;
    top: 0;
    z-index: 10;
    background: var(--ground);
  }
  .bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    box-sizing: border-box;
    height: var(--header-height);
    padding: 10px 8px 10px 20px;
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 2px;
  }
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    padding: 0;
    border: 0;
    border-radius: 22px;
    background: transparent;
    color: var(--text);
    cursor: pointer;
  }
  .icon-btn:hover {
    background: var(--hover);
    color: var(--text);
  }
  .avatar {
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
  .search-row {
    padding: 0 16px 8px;
  }
  .search-row[hidden] {
    display: none;
  }
  .note.on {
    padding: 0 16px 8px;
  }
</style>
