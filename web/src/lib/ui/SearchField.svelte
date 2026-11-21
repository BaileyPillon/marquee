<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import { navigate } from '../core/router';
  import { t } from '../core/i18n';

  interface Props {
    /** Focus the field when it appears (the phone search row). */
    focusOnMount?: boolean;
    /** Called after a search has been submitted. */
    onsubmitted?: () => void;
  }

  let { focusOnMount = false, onsubmitted }: Props = $props();

  const uid = $props.id();
  const inputId = `search-${uid}`;
  let value = $state('');
  let input: HTMLInputElement | undefined = $state();

  onMount(() => {
    if (focusOnMount) input?.focus();
  });

  function submit(event: SubmitEvent): void {
    event.preventDefault();
    const q = value.trim();
    navigate(q ? `/explore?q=${encodeURIComponent(q)}` : '/explore');
    onsubmitted?.();
  }
</script>

<form class="search" role="search" onsubmit={submit}>
  <label class="sr-only" for={inputId}>{t('search.label')}</label>
  <span class="glyph"><Icon name="search" size={18} /></span>
  <input
    id={inputId}
    type="search"
    bind:value
    bind:this={input}
    placeholder={t('search.placeholder')}
    autocomplete="off"
    enterkeyhint="search"
  />
</form>

<style>
  .search {
    position: relative;
    width: 100%;
  }
  .glyph {
    position: absolute;
    left: 14px;
    top: 13px;
    display: flex;
    color: var(--muted);
    pointer-events: none;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    height: 44px;
    padding: 0 14px 0 42px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 14px;
    font-weight: 400;
    line-height: 1;
    color-scheme: dark;
  }
  input::placeholder {
    color: var(--muted);
    opacity: 1;
  }
</style>
