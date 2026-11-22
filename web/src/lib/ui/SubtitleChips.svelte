<script lang="ts">
  import { t } from '../core/i18n';
  import { languageCode, languageName } from '../core/languageNames';
  import type { FileSubtitle } from '../core/subtitlesApi';

  interface Props {
    /** The subtitles of one video file. */
    subtitles: FileSubtitle[];
    /** The wanted languages the file still lacks. */
    missing?: string[];
    /** Say "No subtitles yet" when there is nothing to show. */
    showNone?: boolean;
  }

  let { subtitles, missing = [], showNone = false }: Props = $props();

  interface Chip {
    key: string;
    text: string;
    title: string;
    ours: boolean;
  }

  // One chip for each language and flavour (ordinary, hearing impaired, forced): two files in the same
  // flavour show once.
  const chips = $derived.by<Chip[]>(() => {
    const seen = new Map<string, Chip>();
    for (const s of subtitles) {
      const key = `${s.language}|${s.hi}|${s.forced === true}`;
      const have = seen.get(key);
      if (have) {
        have.ours = have.ours || s.source === 'marquee';
        continue;
      }
      const flags = [s.hi ? t('subs.sheet.hi') : '', s.forced ? t('subs.file.forced') : ''].filter(Boolean);
      const code = s.language ? languageCode(s.language) : t('subs.file.noLanguage');
      const full = [
        s.language ? languageName(s.language) : t('subs.file.noLanguage'),
        s.hi ? t('subs.file.hi') : '',
        s.forced ? t('subs.file.forced') : '',
      ]
        .filter(Boolean)
        .join(', ');
      seen.set(key, {
        key,
        text: [code, ...flags].join(' · '),
        title: full,
        ours: s.source === 'marquee',
      });
    }
    return [...seen.values()];
  });

  const missingText = $derived(missing.map(languageName).join(', '));
</script>

{#if chips.length > 0 || missing.length > 0 || showNone}
  <ul class="subs">
    {#each chips as chip (chip.key)}
      <li class="chip" class:ours={chip.ours} title={chip.title}>
        <span class="sr-only">{chip.title}. </span>
        <span aria-hidden="true">{chip.text}</span>
      </li>
    {/each}
    {#if chips.length === 0 && showNone}
      <li class="none">{t('subs.file.none')}</li>
    {/if}
    {#if missing.length > 0 && chips.length > 0}
      <li class="miss">{t('subs.file.missing', { languages: missingText })}</li>
    {/if}
  </ul>
{/if}

<style>
  .subs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    min-height: 22px;
    padding: 0 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    font-size: 11.5px;
    font-weight: 600;
    letter-spacing: 0.06em;
    line-height: 16px;
    color: var(--text-2);
  }
  .chip.ours {
    color: var(--text);
    border-color: var(--line-strong);
  }
  .none,
  .miss {
    font-size: 12.5px;
    line-height: 16px;
    color: var(--muted);
  }
</style>
