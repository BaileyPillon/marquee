<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import { activeLanguage, availableLanguages, setLanguage, t } from '../core/i18n';
  import { englishName, isRtlLanguage, nativeName } from '../core/languages';

  interface Props {
    /** Lay the choices out in columns (Settings) instead of one list (the sheet). */
    grid?: boolean;
    /** Put the cursor in the search box when this appears (on a phone that would raise the keyboard). */
    focusOnMount?: boolean;
    /** Called after a language was chosen and is showing. */
    onchosen?: (code: string) => void;
  }

  let { grid = false, focusOnMount = false, onchosen }: Props = $props();

  const uid = $props.id();
  const inputId = `lang-search-${uid}`;

  interface Option {
    code: string;
    native: string;
    english: string;
    rtl: boolean;
    /** What the search box looks at: both names and the code, in plain lower case. */
    haystack: string;
  }

  /** "Español" -> "espanol": so a plain keyboard still finds it. */
  const plain = (text: string): string =>
    text.normalize('NFD').replace(/\p{M}/gu, '').toLocaleLowerCase();

  const options: Option[] = availableLanguages()
    .map((code) => {
      const native = nativeName(code);
      const english = englishName(code);
      return {
        code,
        native,
        english,
        rtl: isRtlLanguage(code),
        haystack: plain(`${native} ${english} ${code}`),
      };
    })
    .sort((a, b) => a.english.localeCompare(b.english, 'en'));

  let query = $state('');
  let busy = $state<string | null>(null);
  let failed = $state(false);
  let input: HTMLInputElement | undefined = $state();

  const active = $derived(activeLanguage());
  const shown = $derived.by(() => {
    const wanted = plain(query.trim());
    return wanted ? options.filter((o) => o.haystack.includes(wanted)) : options;
  });

  onMount(() => {
    if (focusOnMount) {
      input?.focus();
      return;
    }
    // A modal dialog hands focus to its first field; on a phone that would raise the keyboard.
    const timer = setTimeout(() => {
      if (document.activeElement === input) input?.blur();
    }, 0);
    return () => clearTimeout(timer);
  });

  async function choose(code: string): Promise<void> {
    if (busy) return;
    if (code === active) {
      onchosen?.(code);
      return;
    }
    busy = code;
    failed = false;
    const ok = await setLanguage(code);
    busy = null;
    if (ok) onchosen?.(code);
    else failed = true;
  }
</script>

<div class="picker">
  <div class="search">
    <label class="sr-only" for={inputId}>{t('lang.search')}</label>
    <span class="glyph"><Icon name="search" size={18} /></span>
    <input
      id={inputId}
      type="search"
      bind:value={query}
      bind:this={input}
      placeholder={t('lang.search')}
      autocomplete="off"
      spellcheck="false"
    />
  </div>

  {#if failed}<p class="problem" role="alert">{t('lang.failed')}</p>{/if}

  {#if shown.length === 0}
    <p class="none" role="status">{t('lang.noMatch', { q: query.trim() })}</p>
  {:else}
    <ul class="list" class:grid aria-label={t('lang.listLabel')}>
      {#each shown as option (option.code)}
        {@const current = option.code === active}
        <li>
          <button
            type="button"
            class="row"
            class:current
            disabled={busy !== null}
            aria-current={current ? 'true' : undefined}
            aria-busy={busy === option.code ? 'true' : undefined}
            onclick={() => choose(option.code)}
          >
            <span class="names">
              <span class="native" lang={option.code} dir={option.rtl ? 'rtl' : 'ltr'}>{option.native}</span>
              {#if option.english !== option.native}<span class="english">{option.english}</span>{/if}
            </span>
            {#if current}
              <span class="mark"><Icon name="check" size={18} stroke={2.4} /><span class="sr-only">{t('lang.current')}</span></span>
            {:else if busy === option.code}
              <span class="mark wait" aria-hidden="true">…</span>
            {/if}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .picker {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-width: 0;
  }
  .search {
    position: relative;
  }
  .glyph {
    position: absolute;
    inset-inline-start: 14px;
    top: 13px;
    display: flex;
    color: var(--muted);
    pointer-events: none;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    height: 44px;
    padding-block: 0;
    padding-inline: 42px 14px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 14px;
    line-height: 1;
    color-scheme: dark;
  }
  input::placeholder {
    color: var(--muted);
    opacity: 1;
  }

  .list {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: min(52dvh, 440px);
    overflow-y: auto;
    overscroll-behavior: contain;
  }
  /* In Settings there is room: show every language at once, in columns. */
  .list.grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 210px), 1fr));
    max-height: none;
    overflow-y: visible;
  }
  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    box-sizing: border-box;
    width: 100%;
    min-height: 52px;
    padding: 8px 12px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text-2);
    font-family: var(--font-sans);
    text-align: start;
    cursor: pointer;
  }
  .row:hover:not(:disabled) {
    background: var(--hover);
    color: var(--text);
  }
  .row.current {
    background: var(--nav-active);
    color: var(--accent);
  }
  .row:disabled {
    cursor: progress;
    opacity: 0.7;
  }
  .names {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .native {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    overflow-wrap: anywhere;
  }
  .english {
    font-size: 12.5px;
    line-height: 16px;
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  .mark {
    display: inline-flex;
    flex: 0 0 auto;
    color: var(--accent);
  }
  .problem {
    font-size: 13.5px;
    line-height: 19px;
    color: var(--danger);
  }
  .none {
    padding: 8px 2px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
</style>
