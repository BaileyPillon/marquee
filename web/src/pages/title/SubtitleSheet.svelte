<script lang="ts">
  import BottomSheet from '../../lib/ui/BottomSheet.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import ErrorNote from '../../lib/accounts/ErrorNote.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import { ApiError } from '../../lib/core/api';
  import { formatNumber, t } from '../../lib/core/i18n';
  import { COMMON_LANGUAGES, languageCode, languageName } from '../../lib/core/languageNames';
  import {
    downloadSubtitle,
    removeSubtitle,
    searchSubtitles,
    type FileSubtitle,
    type SubtitleResult,
    type SubtitleSearch,
  } from '../../lib/core/subtitlesApi';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    fileId: number;
    /** What the dialog is about: the film's name, or an episode's. */
    name: string;
    /** The wanted languages: they come first in the language list, and the first is searched. */
    languages: string[];
    /** What the file has now. */
    subtitles: FileSubtitle[];
    /** A subtitle was saved or removed: the page may look again. */
    onchanged?: () => void;
  }

  let { open = $bindable(false), fileId, name, languages, subtitles, onchanged }: Props = $props();

  const uid = $props.id();
  const haveId = `subs-have-${uid}`;
  const resultsId = `subs-results-${uid}`;

  // ---- the language ----
  const options = $derived.by(() => {
    const rest = COMMON_LANGUAGES.filter((c) => !languages.includes(c));
    return [...languages, ...rest].map((code) => ({ value: code, label: `${languageName(code)} (${languageCode(code)})` }));
  });
  let language = $state('');
  $effect(() => {
    if (!language || !options.some((o) => o.value === language)) language = options[0]?.value ?? 'en';
  });

  // ---- searching ----
  let searching = $state(false);
  let search = $state.raw<SubtitleSearch | undefined>();
  let error = $state('');
  let needsProvider = $state(false);
  let controller: AbortController | undefined;

  function problem(err: unknown): string {
    needsProvider = err instanceof ApiError && err.code === 'no_provider';
    if (err instanceof ApiError) return err.status === 0 ? t('error.network') : err.message || t('subs.sheet.failed');
    return t('subs.sheet.failed');
  }

  async function run(): Promise<void> {
    if (searching) return;
    controller?.abort();
    const mine = (controller = new AbortController());
    searching = true;
    error = '';
    needsProvider = false;
    saved = {};
    try {
      const res = await searchSubtitles(fileId, language, mine.signal);
      if (!mine.signal.aborted) search = res;
    } catch (err) {
      if (!mine.signal.aborted) {
        error = problem(err);
        search = undefined;
      }
    } finally {
      if (!mine.signal.aborted) searching = false;
    }
  }

  // The dialog opens clean each time and stops its request when it closes.
  $effect(() => {
    if (open) return;
    controller?.abort();
    search = undefined;
    error = '';
    needsProvider = false;
    saved = {};
    searching = false;
  });

  // ---- downloading ----
  const keyOf = (r: SubtitleResult): string => `${r.provider}:${r.providerId}`;
  let busy = $state('');
  let saved = $state<Record<string, string>>({});

  async function download(r: SubtitleResult): Promise<void> {
    if (busy) return;
    busy = keyOf(r);
    error = '';
    try {
      const sub = await downloadSubtitle(fileId, r);
      saved[keyOf(r)] = sub.name;
      onchanged?.();
    } catch (err) {
      error = problem(err);
    } finally {
      busy = '';
    }
  }

  // ---- removing one Marquee saved ----
  let removing = $state<number | null>(null);

  async function remove(s: FileSubtitle): Promise<void> {
    if (s.id === undefined || removing !== null) return;
    removing = s.id;
    error = '';
    try {
      await removeSubtitle(s.id);
      onchanged?.();
    } catch (err) {
      error = problem(err);
    } finally {
      removing = null;
    }
  }

  const ordered = $derived(search?.results ?? []);
  // Only the best result that meets the minimum is the main button; the rest are outlined.
  const best = $derived(ordered.find((r) => r.accepted));

  function flavour(s: FileSubtitle): string {
    return [languageName(s.language), s.hi ? t('subs.file.hi') : '', s.forced ? t('subs.file.forced') : ''].filter(Boolean).join(', ');
  }
</script>

<BottomSheet bind:open title={t('subs.sheet.title', { name })} wide>
  <div class="body">
    {#if subtitles.length > 0}
      <h3 id={haveId} class="caps">{t('subs.sheet.have')}</h3>
      <ul class="have" aria-labelledby={haveId}>
        {#each subtitles as s (s.name)}
          <li>
            <span class="file">
              <span class="lang">{s.language ? flavour(s) : s.name}</span>
              <span class="meta">{s.source === 'marquee' ? t('subs.file.byMarquee') : t('subs.file.byFile')} · {s.name}</span>
            </span>
            {#if s.id !== undefined}
              <Button
                variant="quiet"
                size="xs"
                disabled={removing !== null}
                aria-label={t('subs.sheet.removeAria', { language: s.language ? languageName(s.language) : s.name })}
                onclick={() => remove(s)}
              >
                {t('subs.sheet.remove')}
              </Button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}

    <form
      class="ask"
      onsubmit={(event) => {
        event.preventDefault();
        void run();
      }}
    >
      <div class="pick"><SelectField label={t('subs.sheet.language')} bind:value={language} {options} /></div>
      <Button variant="primary" size="lg" type="submit" disabled={searching}>
        <Icon name="search" size={16} stroke={2.2} />
        {searching ? t('subs.sheet.searching') : t('subs.sheet.search')}
      </Button>
    </form>

    {#if error}
      <div class="err">
        <ErrorNote>{error}</ErrorNote>
        {#if needsProvider}
          <Button variant="link" size="xs" href="/settings?tab=subtitles">{t('settings.tab.subtitles')}</Button>
        {/if}
      </div>
    {/if}

    {#if search}
      <h3 id={resultsId} class="caps">{t('subs.sheet.results')}</h3>
      {#each search.providers.filter((p) => !p.ok) as p (p.name)}
        <p class="down">
          {t('subs.sheet.providerDown', {
            name: p.name === 'opensubtitles' ? t('subs.provider.opensubtitles') : p.name === 'podnapisi' ? t('subs.provider.podnapisi') : p.name,
            why: p.error ?? '',
          })}
        </p>
      {/each}
      {#if ordered.length === 0}
        <p class="empty">{t('subs.sheet.empty')}</p>
      {:else}
        <ol class="results" aria-labelledby={resultsId}>
          {#each ordered as r (keyOf(r))}
            {@const done = saved[keyOf(r)] !== undefined}
            <li class:below={!r.accepted}>
              <div class="score" class:ok={r.accepted} title={r.accepted ? t('subs.sheet.fits') : t('subs.sheet.below')}>
                <span class="n">{formatNumber(r.score)}</span>
                <span class="of" aria-hidden="true">/ 100</span>
                <span class="sr-only">{t('subs.sheet.col.score')}</span>
              </div>
              <div class="what">
                <div class="release">{r.release || t('subs.sheet.noRelease')}</div>
                <div class="facts">
                  <span>{r.provider === 'opensubtitles' ? t('subs.provider.opensubtitles') : r.provider === 'podnapisi' ? t('subs.provider.podnapisi') : r.provider}</span>
                  <span>{t('subs.sheet.col.downloads')}: {formatNumber(r.downloads)}</span>
                  {#if r.hashMatch}<span class="tag">{t('subs.sheet.exact')}</span>{/if}
                  {#if r.hi}<span class="tag">{t('subs.sheet.hi')}</span>{/if}
                </div>
                {#if r.reasons.length > 0}<div class="why">{r.reasons.slice(0, 3).join(' · ')}</div>{/if}
                {#if done}<div class="done" role="status">{t('subs.sheet.saved', { name: saved[keyOf(r)] ?? '' })}</div>{/if}
              </div>
              <div class="act">
                <Button
                  variant={r === best && !done ? 'primary' : 'outline'}
                  size="sm"
                  disabled={busy !== '' || done}
                  aria-label={t('subs.sheet.downloadAria', { release: r.release || t('subs.sheet.noRelease') })}
                  onclick={() => download(r)}
                >
                  {#if done}
                    <Icon name="check" size={16} stroke={2.4} />
                  {:else}
                    <Icon name="downloads" size={16} stroke={2.2} />
                  {/if}
                  {busy === keyOf(r) ? t('subs.sheet.downloading') : t('subs.sheet.download')}
                </Button>
              </div>
            </li>
          {/each}
        </ol>
      {/if}
    {/if}
  </div>
  <div class="close">
    <Button variant="ghost" size="lg" block onclick={() => (open = false)}>{t('ask.close')}</Button>
  </div>
</BottomSheet>

<style>
  .body {
    margin-top: 14px;
  }
  .caps {
    margin: 18px 0 8px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .caps:first-child {
    margin-top: 0;
  }
  .have,
  .results {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .have li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 12px;
    border-radius: var(--radius-md);
    background: var(--rail);
    box-shadow: inset 0 0 0 1px var(--hairline);
  }
  .file {
    min-width: 0;
  }
  .lang {
    display: block;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .meta {
    display: block;
    font-size: 12.5px;
    line-height: 17px;
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  .ask {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 10px 12px;
    margin-top: 18px;
  }
  .pick {
    flex: 1 1 220px;
    max-width: 360px;
  }
  .err {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
    margin-top: 12px;
  }
  .down,
  .empty {
    margin: 0 0 10px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  .results li {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr) auto;
    align-items: center;
    gap: 12px;
    padding: 12px;
    border-radius: var(--radius-md);
    background: var(--panel);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
  }
  .results li.below {
    opacity: 0.78;
  }
  .score {
    display: flex;
    flex-direction: column;
    align-items: center;
    line-height: 1.1;
    color: var(--text-3);
  }
  .score.ok {
    color: var(--text);
  }
  .n {
    font-size: 22px;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }
  .of {
    font-size: 10.5px;
    letter-spacing: 0.06em;
    color: var(--muted);
  }
  .release {
    font-size: 14px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .facts {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 12px;
    margin-top: 2px;
    font-size: 12.5px;
    line-height: 17px;
    color: var(--muted);
  }
  .tag {
    font-weight: 600;
    letter-spacing: 0.04em;
    color: var(--text-2);
  }
  .why {
    margin-top: 4px;
    font-size: 12.5px;
    line-height: 17px;
    color: var(--text-3);
  }
  .done {
    margin-top: 4px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .close {
    margin-top: 16px;
  }
  @media (max-width: 560px) {
    .results li {
      grid-template-columns: 52px minmax(0, 1fr);
    }
    .act {
      grid-column: 1 / -1;
    }
  }
</style>
