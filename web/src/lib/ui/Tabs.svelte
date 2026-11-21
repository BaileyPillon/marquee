<script module lang="ts">
  export interface TabItem {
    id: string;
    label: string;
  }
</script>

<script lang="ts">
  interface Props {
    items: TabItem[];
    /** The id of the selected item. */
    value: string;
    onchange: (id: string) => void;
    /** Names the group for assistive tech. */
    label: string;
    /**
     * tabs: underlined tabs that switch a panel (role=tablist, aria-selected, arrow keys). The
     * panel needs role=tabpanel, id `{prefix}-panel` and aria-labelledby `{prefix}-tab-{id}`.
     * nav: a row of buttons that choose a section (aria-current), as in Settings.
     */
    kind?: 'tabs' | 'nav';
    /** Prefix for the element ids, so a panel can point back at its tab. */
    prefix: string;
  }

  let { items, value, onchange, label, kind = 'tabs', prefix }: Props = $props();

  let list: HTMLElement | undefined = $state();

  /** Arrow keys, Home and End move between tabs and select the one that gets focus. */
  function onkeydown(event: KeyboardEvent): void {
    if (kind !== 'tabs') return;
    const index = items.findIndex((item) => item.id === value);
    let next = index;
    if (event.key === 'ArrowRight') next = (index + 1) % items.length;
    else if (event.key === 'ArrowLeft') next = (index - 1 + items.length) % items.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = items.length - 1;
    else return;
    event.preventDefault();
    const target = items[next];
    if (!target) return;
    onchange(target.id);
    list?.querySelector<HTMLElement>(`[data-tab="${target.id}"]`)?.focus();
  }
</script>

{#snippet buttons()}
  {#each items as item (item.id)}
    {@const selected = item.id === value}
    {#if kind === 'tabs'}
      <button
        type="button"
        class="tab"
        class:selected
        role="tab"
        id="{prefix}-tab-{item.id}"
        data-tab={item.id}
        aria-selected={selected}
        aria-controls="{prefix}-panel"
        tabindex={selected ? 0 : -1}
        {onkeydown}
        onclick={() => onchange(item.id)}>{item.label}</button
      >
    {:else}
      <button
        type="button"
        class="pill"
        class:selected
        data-tab={item.id}
        aria-current={selected ? 'page' : undefined}
        onclick={() => onchange(item.id)}>{item.label}</button
      >
    {/if}
  {/each}
{/snippet}

{#if kind === 'tabs'}
  <div class="tabs" role="tablist" aria-label={label} bind:this={list}>
    {@render buttons()}
  </div>
{:else}
  <nav class="nav" aria-label={label} bind:this={list}>
    {@render buttons()}
  </nav>
{/if}

<style>
  .tabs {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    border-bottom: 1px solid var(--hairline);
  }
  .tab {
    display: inline-flex;
    align-items: center;
    box-sizing: border-box;
    min-height: 46px;
    margin: 0 0 -1px;
    padding: 0 14px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--text-3);
    font-size: 14.5px;
    font-weight: 500;
    line-height: 1;
    cursor: pointer;
  }
  .tab:hover {
    color: var(--text);
  }
  .tab.selected {
    border-bottom-color: var(--accent);
    color: var(--text);
    font-weight: 600;
  }

  .nav {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--hairline);
  }
  .pill {
    display: inline-flex;
    align-items: center;
    box-sizing: border-box;
    height: 38px;
    padding: 0 14px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text-2);
    font-size: 14px;
    font-weight: 500;
    line-height: 1;
    cursor: pointer;
  }
  .pill:hover {
    background: var(--hover);
    color: var(--text);
  }
  .pill.selected {
    background: var(--nav-active);
    color: var(--accent);
    font-weight: 600;
  }
</style>
