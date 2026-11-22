// Shared by extract-check.mjs, translate.mjs and validate.mjs.
//
// Layout (all paths from web/):
//   src/lib/core/i18n/en.json        the English source: the one file humans and other agents edit
//   src/lib/core/i18n/<code>.json    SHIPPED catalogs. A file here is a promise: 100% of the keys, validated.
//   scripts/i18n/wip/<code>.json     catalogs still being translated (never shipped, never bundled)
//   scripts/i18n/work/               per-language work files written by `translate.mjs prepare`
//   scripts/i18n/glossary/<code>.json  the agreed words of the product, per language
//   scripts/i18n/manifest.json       which languages exist and in what order they are done
//   scripts/i18n/.state.json         sha of each English string as it was when it was translated
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse, shapeOf } from '../../src/lib/core/icu.ts';

export const here = path.dirname(fileURLToPath(import.meta.url));
export const webRoot = path.resolve(here, '../..');
export const srcDir = path.join(webRoot, 'src');
export const catalogDir = path.join(srcDir, 'lib', 'core', 'i18n');
export const wipDir = path.join(here, 'wip');
export const workDir = path.join(here, 'work');
export const glossaryDir = path.join(here, 'glossary');
export const statePath = path.join(here, '.state.json');
export const manifestPath = path.join(here, 'manifest.json');

// ---- files ---------------------------------------------------------------------------------

export function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

/** Catalogs are flat objects, two-space indented, one key per line, ending in a newline. */
export function writeCatalog(file, catalog) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${JSON.stringify(catalog, null, 2)}\n`);
}

export function loadManifest() {
  return readJson(manifestPath);
}

/** English in file order. */
export function loadEnglish() {
  return readJson(path.join(catalogDir, 'en.json'));
}

export const shippedPath = (code) => path.join(catalogDir, `${code}.json`);
export const wipPath = (code) => path.join(wipDir, `${code}.json`);

/** Where a language's catalog lives right now: shipped if it is, else wip, else nowhere yet. */
export function locateCatalog(code) {
  if (fs.existsSync(shippedPath(code))) return { code, shipped: true, file: shippedPath(code) };
  if (fs.existsSync(wipPath(code))) return { code, shipped: false, file: wipPath(code) };
  return { code, shipped: false, file: wipPath(code), missing: true };
}

/** Every language that has a shipped catalog on disk, whether or not the manifest knows it. */
export function shippedCodes() {
  return fs
    .readdirSync(catalogDir)
    .filter((f) => f.endsWith('.json') && f !== 'en.json')
    .map((f) => f.slice(0, -'.json'.length))
    .sort();
}

// ---- state ---------------------------------------------------------------------------------

export const sha = (text) => crypto.createHash('sha1').update(text).digest('hex').slice(0, 12);

// In memory: state.languages[code][key] = sha of the English the translation answered.
// On disk it is stored the other way round, one line per key, so it stays small:
//   { "nav.home": { "a1b2c3d4e5f6": ["ar", "de", "es", ...] } }
// (languages that were translated from the same English text share one entry).
export function loadState() {
  const state = { version: 2, languages: {} };
  if (!fs.existsSync(statePath)) return state;
  const disk = readJson(statePath);
  for (const [key, byHash] of Object.entries(disk.keys ?? {})) {
    for (const [hash, codes] of Object.entries(byHash)) {
      for (const code of codes) (state.languages[code] ??= {})[key] = hash;
    }
  }
  return state;
}

export function saveState(state) {
  const keys = {};
  for (const [code, known] of Object.entries(state.languages)) {
    for (const [key, hash] of Object.entries(known)) ((keys[key] ??= {})[hash] ??= []).push(code);
  }
  const lines = Object.keys(keys)
    .sort()
    .map((key) => {
      const byHash = Object.fromEntries(
        Object.entries(keys[key]).map(([hash, codes]) => [hash, codes.sort()]),
      );
      return `    ${JSON.stringify(key)}: ${JSON.stringify(byHash)}`;
    });
  fs.writeFileSync(statePath, `{\n  "version": 2,\n  "keys": {\n${lines.join(',\n')}\n  }\n}\n`);
}

// ---- checking a translated message -----------------------------------------------------------

/** Names that must survive translation exactly (case and all) whenever the English has them. */
const KEEP = [
  'Marquee', 'TMDB', 'TVmaze', 'MusicBrainz', 'Plex', 'Jellyfin', 'Emby', 'Prowlarr', 'Jackett',
  'Torznab', 'Newznab', 'qBittorrent', 'Deluge', 'SABnzbd', 'Discord', 'Telegram', 'Claude', 'MCP',
  'HDR', 'SMTP', 'YouTube', 'Docker', 'Usenet', 'X-Plex-Token', 'Bluray-1080p', '4K', 'HD',
];

/** Pieces of text that are code, not language: addresses, paths, handles. */
const LITERALS = [
  /https?:\/\/[^\s,)”"]*[^\s,.)”"]/g,
  /\b[A-Za-z]:\\[A-Za-z0-9_\\]+/g,
  /\/media\/[a-z]+/g,
  /<[a-z]+>/g,
  /@[A-Za-z]\w+/g,
  /\b\d{3,4}p\b/g,
];

// A name must be there, spelled as it is. A case ending after it is fine: German "Claudes",
// Polish "w Dockerze", Russian "в Docker-е" (so only the start of the word is checked).
const words = (text, token) =>
  new RegExp(`(?<![A-Za-z0-9])${token.replace(/[-/\\^$*+?.()|[\]{}]/g, '\\$&')}`).test(text);

/** The plural categories a language needs: those that real counts (0 to 1000) land on, plus "other". */
export function neededCategories(tag) {
  const rules = new Intl.PluralRules(tag);
  const seen = new Set(['other']);
  for (let n = 0; n <= 1000; n += 1) seen.add(rules.select(n));
  return seen;
}

/**
 * Compare one translation against its English message.
 * Returns { errors: string[], warnings: string[] }. An error stops a language from shipping.
 */
export function checkMessage(code, key, english, translation) {
  const errors = [];
  const warnings = [];
  if (typeof translation !== 'string') return { errors: ['not a string'], warnings };
  if (translation.trim() === '') return { errors: ['empty'], warnings };

  let en;
  let tr;
  try {
    en = shapeOf(english);
  } catch (err) {
    return { errors: [`the English message is broken: ${err.message}`], warnings };
  }
  try {
    tr = shapeOf(translation);
  } catch (err) {
    return { errors: [`ICU syntax: ${err.message}`], warnings };
  }

  // Same placeholders.
  const lacking = [...en.names].filter((n) => !tr.names.has(n));
  const extra = [...tr.names].filter((n) => !en.names.has(n));
  if (lacking.length) errors.push(`missing placeholder ${lacking.map((n) => `{${n}}`).join(' ')}`);
  if (extra.length) errors.push(`unknown placeholder ${extra.map((n) => `{${n}}`).join(' ')}`);

  // Same plural and select structure.
  const tag = code;
  const needed = neededCategories(tag);
  for (const block of en.blocks) {
    const mine = tr.blocks.filter((b) => b.name === block.name && b.kind === block.kind);
    if (block.kind === 'select') {
      if (mine.length === 0) errors.push(`select on {${block.name}} is gone`);
      for (const b of mine) {
        const want = [...block.cases].sort().join(',');
        const got = [...b.cases].sort().join(',');
        if (want !== got) errors.push(`select on {${block.name}} has cases (${got}) but English has (${want})`);
      }
      continue;
    }
    // A plural. A language with only "other" may write the number plainly instead.
    if (mine.length === 0 && needed.size > 1) errors.push(`needs a plural on {${block.name}} (${[...needed].join(', ')})`);
  }
  for (const b of tr.blocks) {
    if (b.kind !== 'plural') continue;
    const have = new Set(b.cases.filter((c) => !c.startsWith('=')));
    // A block with only "other" is the number written plainly (a way to avoid agreement): allowed.
    const plainOnly = have.size === 1 && have.has('other');
    if (!plainOnly) {
      for (const c of needed) if (!have.has(c)) errors.push(`plural on {${b.name}} lacks "${c}" (${tag} needs ${[...needed].join(', ')})`);
    }
    for (const c of have) {
      if (!['zero', 'one', 'two', 'few', 'many', 'other'].includes(c)) errors.push(`plural on {${b.name}}: "${c}" is not a plural category`);
      else if (!needed.has(c) && !(c === 'many' && new Intl.PluralRules(tag).resolvedOptions().pluralCategories.includes('many'))) {
        warnings.push(`plural on {${b.name}}: "${c}" is never used in ${tag}`);
      }
    }
  }
  // A select the English does not have is a structure the caller cannot drive.
  for (const b of tr.blocks) {
    if (b.kind === 'select' && !en.blocks.some((e) => e.kind === 'select' && e.name === b.name)) {
      errors.push(`select on {${b.name}} is not in English`);
    }
  }

  // Names and code that must stay as they are.
  for (const token of KEEP) {
    if (words(english, token) && !words(translation, token)) errors.push(`"${token}" must stay as it is`);
  }
  for (const re of LITERALS) {
    for (const found of english.match(re) ?? []) {
      if (!translation.includes(found)) errors.push(`"${found}" must stay as it is`);
    }
  }

  // Softer signs of a slip.
  if (english.endsWith('…') && !translation.endsWith('…') && !translation.endsWith('...')) {
    warnings.push('English ends in … and the translation does not');
  }
  if (translation !== translation.trim() && english === english.trim()) warnings.push('stray space at an end');
  if (/\s{2,}/.test(translation) && !/\s{2,}/.test(english)) warnings.push('double space');
  // (a message with plural forms is long by nature: all the forms are in the one string)
  if (english.length >= 14 && !translation.includes(', plural,') && translation.length > english.length * 2.6) {
    warnings.push(`long: ${translation.length} characters for ${english.length}`);
  }
  return { errors, warnings };
}

/**
 * Typographic habits that are tedious to type by hand, applied to every line `translate.mjs apply`
 * takes in. French sets a non-breaking space before : % and a narrow one before ? ! ; » and inside « ».
 */
export function typography(code, text) {
  if (code !== 'fr') return text;
  return text
    .replace(/[  ]+([?!;»])/g, ' $1')
    .replace(/[  ]+([:%])/g, ' $1')
    .replace(/(«)[  ]+/g, '$1 ');
}

// ---- helpers for the reports ---------------------------------------------------------------

export const pct = (n, total) => (total === 0 ? '100.0' : ((n * 100) / total).toFixed(1));

export function pad(text, width) {
  const s = String(text);
  return s.length >= width ? s : s + ' '.repeat(width - s.length);
}

/** Exported for the key checker: the placeholder names an English message needs from its caller. */
export function placeholderNames(message) {
  try {
    return shapeOf(message).names;
  } catch {
    return new Set();
  }
}

export { parse, shapeOf };
