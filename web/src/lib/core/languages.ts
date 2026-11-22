// What the runtime needs to know about language codes: direction, the Intl tag, which catalog a
// browser language should land on, and how to show a language's name. Which languages exist at all
// is decided by the catalog files in ./i18n (see i18n.ts), not by this file.

/** Languages written right to left (by base language). */
const RTL_BASES = new Set(['ar', 'he', 'fa', 'ur']);

/** The part before the first dash: "pt-BR" -> "pt". */
export function baseOf(code: string): string {
  return code.split('-')[0] ?? code;
}

export function isRtlLanguage(code: string): boolean {
  return RTL_BASES.has(baseOf(code));
}

/** The tag to hand to Intl. Catalog codes are valid BCP 47 already; this only guards a bad one. */
export function intlTag(code: string): string {
  try {
    return Intl.getCanonicalLocales(code)[0] ?? 'en';
  } catch {
    return 'en';
  }
}

// Browser tags that mean a language we name differently.
const ALIASES: Record<string, string> = {
  no: 'nb',
  nn: 'nb',
  iw: 'he',
  tl: 'fil',
  in: 'id',
  ji: 'yi',
  mo: 'ro',
  sh: 'sr',
};

// Regions where "es" means Latin American Spanish, and where "pt" means European Portuguese.
const ES_419 = new Set([
  '419', 'AR', 'BO', 'CL', 'CO', 'CR', 'CU', 'DO', 'EC', 'GT', 'HN', 'MX', 'NI', 'PA', 'PE', 'PR', 'PY', 'SV', 'US', 'UY', 'VE',
]);
const PT_PT = new Set(['PT', 'AO', 'MZ', 'CV', 'GW', 'ST', 'TL', 'LU', 'CH', 'GQ']);

/** The catalogs a browser language tag can land on, best first. */
function candidates(tag: string): string[] {
  let locale: Intl.Locale;
  try {
    locale = new Intl.Locale(tag);
  } catch {
    return [];
  }
  const base = ALIASES[locale.language] ?? locale.language;
  const region = locale.region ?? '';
  const out: string[] = [tag];
  if (base === 'zh') {
    // Script first; without one, the region tells (Taiwan, Hong Kong and Macau write Traditional).
    const script = locale.script ?? (['TW', 'HK', 'MO'].includes(region) ? 'Hant' : 'Hans');
    out.push(`zh-${script}`);
  } else if (base === 'pt') {
    out.push(PT_PT.has(region) ? 'pt-PT' : 'pt-BR', 'pt-BR');
  } else if (base === 'es') {
    if (ES_419.has(region)) out.push('es-419');
    out.push('es');
  } else {
    out.push(base);
  }
  return out;
}

/**
 * Pick the catalog for the browser's language list (navigator.languages), or undefined when none of
 * them has one. Case does not matter; "de-AT" lands on "de", "zh-TW" on "zh-Hant".
 */
export function matchPreferred(preferred: readonly string[], available: ReadonlySet<string>): string | undefined {
  const byLower = new Map<string, string>();
  for (const code of available) byLower.set(code.toLowerCase(), code);
  for (const tag of preferred) {
    for (const want of candidates(tag)) {
      const hit = byLower.get(want.toLowerCase());
      if (hit) return hit;
    }
  }
  return undefined;
}

/** A language's name in that language, first letter capital: "Deutsch", "日本語", "Português (Brasil)". */
export function nativeName(code: string): string {
  try {
    const name = new Intl.DisplayNames([intlTag(code)], { type: 'language' }).of(code) ?? code;
    return name.charAt(0).toLocaleUpperCase(intlTag(code)) + name.slice(1);
  } catch {
    return code;
  }
}

/** A language's English name, for the search box ("Japanese" finds 日本語). */
export function englishName(code: string): string {
  try {
    return new Intl.DisplayNames(['en'], { type: 'language' }).of(code) ?? code;
  } catch {
    return code;
  }
}
