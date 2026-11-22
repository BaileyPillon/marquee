// Language names for the subtitle pickers: the browser knows them in every interface language, so no
// list of names is shipped. Codes are the ones the server stores: lower case, "pt-br", "zh-cn".
import { activeLocale } from './i18n';

/** The languages offered in the picker, roughly by how many people read subtitles in them. */
export const COMMON_LANGUAGES: readonly string[] = [
  'en', 'es', 'fr', 'de', 'it', 'pt', 'pt-br', 'nl', 'sv', 'no', 'da', 'fi', 'pl', 'cs', 'hu', 'ro', 'el', 'tr',
  'ru', 'uk', 'bg', 'hr', 'sr', 'he', 'ar', 'hi', 'th', 'id', 'vi', 'ja', 'ko', 'zh-cn', 'zh-tw',
];

/** "pt-br" -> "pt-BR": the form Intl wants. */
function tagOf(code: string): string {
  const [language = '', region] = code.split('-');
  return region ? `${language}-${region.toUpperCase()}` : language;
}

/** The name of a language in the interface language ("French", "Français"); the code when unknown. Reactive like t(). */
export function languageName(code: string): string {
  try {
    const names = new Intl.DisplayNames([activeLocale()], { type: 'language' });
    return names.of(tagOf(code)) ?? code.toUpperCase();
  } catch {
    return code.toUpperCase();
  }
}

/** The short label on a chip: "EN", "PT-BR". */
export function languageCode(code: string): string {
  return code.toUpperCase();
}
