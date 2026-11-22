// Translations and locale-aware formatting. Every user-visible string goes through t().
//
// Catalogs: en.json is in the main bundle; every other language is its own file in ./i18n and is
// fetched on demand (Vite turns each into a separate chunk). A language is offered only when its
// catalog file exists, so an unfinished translation simply isn't shipped.
//
// Messages are ICU-like (see icu.ts): "{name}" values and "{n, plural, one {..} other {..}}".
// Lookup order for a key: the chosen language, then its base language ("es" for "es-419"), then English.
import en from './i18n/en.json';
import { format as formatMessage, type FormatContext, type PluralCategory } from './icu';
import { baseOf, intlTag, isRtlLanguage, matchPreferred } from './languages';
import { localeState } from './locale.svelte';

export type MessageKey = keyof typeof en;
export type MessageParams = Record<string, string | number>;
type Catalog = Record<string, string>;

/** Where the person's own choice is kept (this browser only: the server has no language field). */
export const LANGUAGE_STORAGE_KEY = 'marquee.lang';

// ---- catalogs --------------------------------------------------------------------------------

const loaders = import.meta.glob<Catalog>(['./i18n/*.json', '!./i18n/en.json'], { import: 'default' });
const loaderByCode = new Map<string, () => Promise<Catalog>>();
for (const [path, load] of Object.entries(loaders)) {
  const code = /\/([^/]+)\.json$/.exec(path)?.[1];
  if (code) loaderByCode.set(code, load);
}

/** The language codes that have a catalog, English first. */
export function availableLanguages(): string[] {
  return ['en', ...[...loaderByCode.keys()].sort()];
}

interface Entry {
  lang: string;
  messages: Catalog;
  ctx: FormatContext;
}

function entryFor(lang: string, messages: Catalog): Entry {
  const tag = intlTag(lang);
  const rules = new Intl.PluralRules(tag);
  const numbers = new Intl.NumberFormat(tag);
  return {
    lang,
    messages,
    ctx: { plural: (n) => rules.select(n) as PluralCategory, number: (n) => numbers.format(n) },
  };
}

const loaded = new Map<string, Catalog>([['en', en]]);
let chain: Entry[] = [entryFor('en', en)];
let rtl = false;

async function catalogOf(code: string): Promise<Catalog> {
  const have = loaded.get(code);
  if (have) return have;
  const load = loaderByCode.get(code);
  if (!load) throw new Error(`no catalog for "${code}"`);
  const messages = await load();
  loaded.set(code, messages);
  return messages;
}

async function chainFor(code: string): Promise<Entry[]> {
  if (code === 'en') return [entryFor('en', en)];
  const entries = [entryFor(code, await catalogOf(code))];
  const base = baseOf(code);
  if (base !== code && base !== 'en' && loaderByCode.has(base)) {
    entries.push(entryFor(base, await catalogOf(base)));
  }
  entries.push(entryFor('en', en));
  return entries;
}

// ---- t() -------------------------------------------------------------------------------------

const warned = new Set<string>();
function warnOnce(id: string, message: string): void {
  if (!import.meta.env.DEV || warned.has(id)) return;
  warned.add(id);
  console.warn(`[i18n] ${message}`);
}

/** Look up a message and fill it in: `{name}` values, plurals and selects. */
export function t(key: MessageKey, params?: MessageParams): string {
  void localeState.version; // reactive: formatting inside a $derived re-runs when the language changes
  for (const entry of chain) {
    const raw = entry.messages[key];
    if (raw === undefined) continue;
    try {
      return formatMessage(raw, params, entry.ctx);
    } catch (err) {
      warnOnce(`${entry.lang}:${key}`, `${entry.lang} "${key}" is not a valid message: ${String(err)}`);
      return raw;
    }
  }
  warnOnce(`en:${key}`, `"${key}" is not in en.json`);
  return key;
}

// ---- the language in use ---------------------------------------------------------------------

/** The catalog code in use: "en", "de", "pt-BR". Reactive like t(). */
export function activeLanguage(): string {
  void localeState.version;
  return localeState.code;
}

/** The tag for Intl.* in the language in use. Reactive like t(). */
export function activeLocale(): string {
  void localeState.version;
  return intlTag(localeState.code);
}

/** Is the language in use written right to left? Reactive like t(). */
export function isRtl(): boolean {
  void localeState.version;
  return rtl;
}

function apply(code: string): void {
  rtl = isRtlLanguage(code);
  const root = document.documentElement;
  root.lang = code;
  root.dir = rtl ? 'rtl' : 'ltr';
  localeState.code = code;
  localeState.version += 1;
}

let latest = 0;

/**
 * Switch the interface to a language. Downloads its catalog first, so nothing flashes in English.
 * Resolves false when the catalog could not be loaded (the language in use stays), and also when a
 * newer call replaced this one.
 */
export async function setLanguage(code: string, options: { persist?: boolean } = {}): Promise<boolean> {
  const mine = ++latest;
  let next: Entry[];
  try {
    next = await chainFor(code);
  } catch {
    return false;
  }
  if (mine !== latest) return false;
  chain = next;
  apply(code);
  if (options.persist ?? true) {
    try {
      localStorage.setItem(LANGUAGE_STORAGE_KEY, code);
    } catch {
      /* private mode or blocked storage: the choice lasts until the tab closes */
    }
  }
  return true;
}

function savedLanguage(): string | undefined {
  try {
    const code = localStorage.getItem(LANGUAGE_STORAGE_KEY);
    return code && (code === 'en' || loaderByCode.has(code)) ? code : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Choose the starting language: the saved choice, else the browser's language list, else English.
 * Call once before the first render. A browser-detected language is not saved, so it keeps following
 * the browser until the person picks one.
 */
export async function initLanguage(): Promise<void> {
  const prefer = navigator.languages?.length ? navigator.languages : [navigator.language];
  const detected = matchPreferred(prefer, new Set(availableLanguages()));
  const wanted = savedLanguage() ?? detected ?? 'en';
  if (wanted !== 'en' && (await setLanguage(wanted, { persist: false }))) return;
  apply('en');
}

// ---- Intl helpers ----------------------------------------------------------------------------

const formatters = new Map<string, unknown>();

function cached<T>(kind: string, make: (locale: string) => T, options: unknown): T {
  const locale = activeLocale();
  const id = `${kind}|${locale}|${options === undefined ? '' : JSON.stringify(options)}`;
  let found = formatters.get(id) as T | undefined;
  if (found === undefined) {
    found = make(locale);
    formatters.set(id, found);
  }
  return found;
}

/** 1234.5 -> "1,234.5" in English, "1.234,5" in German. Pass Intl options for units, percent, digits. */
export function formatNumber(value: number, options?: Intl.NumberFormatOptions): string {
  return cached('number', (l) => new Intl.NumberFormat(l, options), options).format(value);
}

/** A date or time in the language in use. Pass the Intl options for what to show. */
export function formatDate(value: Date | number, options?: Intl.DateTimeFormatOptions): string {
  return cached('date', (l) => new Intl.DateTimeFormat(l, options), options).format(value);
}

/** "5 min. ago", "in 3 hr.": a relative time in the language in use. Negative is the past. */
export function formatRelative(
  value: number,
  unit: Intl.RelativeTimeFormatUnit,
  options: Intl.RelativeTimeFormatOptions = { numeric: 'always', style: 'short' },
): string {
  return cached('relative', (l) => new Intl.RelativeTimeFormat(l, options), options).format(value, unit);
}

/** ["a", "b", "c"] -> "a, b, and c" in English; "a、b、c" in Japanese. */
export function formatList(items: string[], type: 'conjunction' | 'disjunction' | 'unit' = 'conjunction'): string {
  const options = { style: 'long', type } as const;
  return cached('list', (l) => new Intl.ListFormat(l, options), options).format(items);
}

/** Lower-case a string by the language's own rules ("I" in Turkish). */
export function lowerCase(text: string): string {
  return text.toLocaleLowerCase(activeLocale());
}
