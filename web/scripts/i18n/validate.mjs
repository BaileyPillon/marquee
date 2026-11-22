// Checks every catalog against English: valid JSON, the same keys, no empty strings, the same
// placeholders and the same plural/select structure, names and code left alone. Prints coverage per
// language. Exit code 1 when a SHIPPED catalog has any error or is not 100% complete.
//
//   node scripts/i18n/validate.mjs                 shipped catalogs (src/lib/core/i18n/*.json)
//   node scripts/i18n/validate.mjs --lang de,ja    only those
//   node scripts/i18n/validate.mjs --wip           also report catalogs still in scripts/i18n/wip
//   node scripts/i18n/validate.mjs --verbose       list every problem (the default shows the first few per language)
//   node scripts/i18n/validate.mjs --warnings      list the warnings too
import fs from 'node:fs';
import path from 'node:path';
import {
  catalogDir, checkMessage, glossaryDir, loadEnglish, loadManifest, locateCatalog, pad, pct, readJson, shippedCodes, wipDir,
} from './lib.mjs';

const args = process.argv.slice(2);
const flag = (name) => args.includes(`--${name}`);
const option = (name) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : undefined;
};

const manifest = loadManifest();
const english = loadEnglish();
const englishKeys = Object.keys(english);
const problems = { shipped: 0, any: 0 };

/** Keys that appear twice in a JSON file (JSON.parse would silently keep the last). */
function duplicateKeys(file) {
  const seen = new Set();
  const dups = [];
  for (const line of fs.readFileSync(file, 'utf8').split('\n')) {
    const m = /^\s*"((?:[^"\\]|\\.)*)"\s*:/.exec(line);
    if (!m) continue;
    const key = JSON.parse(`"${m[1]}"`);
    if (seen.has(key)) dups.push(key);
    seen.add(key);
  }
  return dups;
}

// English itself must be sound before anything is compared to it.
{
  const errors = [];
  for (const d of duplicateKeys(path.join(catalogDir, 'en.json'))) errors.push(`en.json: duplicate key "${d}"`);
  for (const [key, message] of Object.entries(english)) {
    if (typeof message !== 'string' || message.trim() === '') errors.push(`en.json: "${key}" is empty`);
    else {
      const self = checkMessage('en', key, message, message);
      if (self.errors.length) errors.push(`en.json: "${key}": ${self.errors.join('; ')}`);
    }
  }
  if (errors.length) {
    console.error(errors.join('\n'));
    process.exit(1);
  }
}

const wanted = option('lang')?.split(',').map((s) => s.trim()).filter(Boolean);
const shipped = new Set(shippedCodes());
const inWip = fs.existsSync(wipDir)
  ? fs.readdirSync(wipDir).filter((f) => f.endsWith('.json')).map((f) => f.slice(0, -5))
  : [];
let codes = wanted ?? [...shipped, ...(flag('wip') ? inWip.filter((c) => !shipped.has(c)) : [])];
codes = [...new Set(codes)].sort();

const rows = [];
const details = [];
for (const code of codes) {
  const where = locateCatalog(code);
  const row = { code, state: where.missing ? 'none' : where.shipped ? 'shipped' : 'wip', keys: 0, errors: 0, warnings: 0, missing: 0 };
  const errors = [];
  const warnings = [];
  rows.push(row);
  if (where.missing) {
    errors.push('no catalog file');
    row.errors = 1;
    details.push({ code, errors, warnings });
    continue;
  }
  let catalog;
  try {
    catalog = readJson(where.file);
  } catch (err) {
    errors.push(`invalid JSON: ${err.message}`);
    row.errors = 1;
    details.push({ code, errors, warnings });
    continue;
  }
  if (catalog === null || typeof catalog !== 'object' || Array.isArray(catalog)) {
    errors.push('the catalog must be one JSON object of strings');
    row.errors = 1;
    details.push({ code, errors, warnings });
    continue;
  }
  for (const d of duplicateKeys(where.file)) errors.push(`duplicate key "${d}"`);
  const keys = Object.keys(catalog);
  const glossary = (() => {
    const file = path.join(glossaryDir, `${code}.json`);
    return fs.existsSync(file) ? readJson(file) : undefined;
  })();
  if (!glossary) warnings.push('no glossary file');

  for (const key of englishKeys) {
    if (!(key in catalog)) {
      row.missing += 1;
      errors.push(`${key}: missing`);
      continue;
    }
    row.keys += 1;
    const { errors: e, warnings: w } = checkMessage(code, key, english[key], catalog[key]);
    for (const m of e) errors.push(`${key}: ${m}`);
    for (const m of w) warnings.push(`${key}: ${m}`);
    // Words the glossary rules out ("avoid"): the catalog drifted from the agreed term.
    for (const term of Object.values(glossary?.terms ?? {})) {
      for (const bad of term.avoid ?? []) {
        if (catalog[key].toLocaleLowerCase(code).includes(bad.toLocaleLowerCase(code))) {
          warnings.push(`${key}: uses "${bad}" (glossary: ${term.use})`);
        }
      }
    }
    // Left in English: fine for names and short tokens, worth a look otherwise.
    if (code !== 'en' && catalog[key] === english[key] && /[a-z]{4,}/.test(english[key]) && !/^[A-Z0-9 ()·.,'’–-]+$/.test(english[key])) {
      const allowed = glossary?.sameAsEnglish ?? [];
      if (!allowed.includes(key)) warnings.push(`${key}: identical to English`);
    }
  }
  for (const key of keys) if (!(key in english)) errors.push(`${key}: not in en.json (obsolete)`);

  row.errors = errors.length;
  row.warnings = warnings.length;
  details.push({ code, errors, warnings });
  problems.any += errors.length;
  if (where.shipped) problems.shipped += errors.length;
}

const manifestCodes = new Set(manifest.languages.map((l) => l.code));
console.log(`English has ${englishKeys.length} keys.\n`);
console.log(`${pad('language', 10)}${pad('state', 9)}${pad('keys', 7)}${pad('coverage', 11)}${pad('errors', 8)}warnings`);
for (const r of rows) {
  console.log(`${pad(r.code, 10)}${pad(r.state, 9)}${pad(r.keys, 7)}${pad(`${pct(r.keys, englishKeys.length)}%`, 11)}${pad(r.errors, 8)}${r.warnings}`);
}
const notInManifest = codes.filter((c) => !manifestCodes.has(c));
if (notInManifest.length) console.log(`\nNot in manifest.json: ${notInManifest.join(', ')}`);
const unshippedPlanned = manifest.languages.filter((l) => !shipped.has(l.code) && !codes.includes(l.code));
if (!wanted && unshippedPlanned.length) {
  console.log(`\nNot shipped yet (${unshippedPlanned.length}): ${unshippedPlanned.map((l) => l.code).join(', ')}`);
}

const limit = flag('verbose') ? Infinity : 6;
for (const d of details) {
  if (d.errors.length === 0 && !(flag('warnings') && d.warnings.length)) continue;
  console.log(`\n${d.code}`);
  for (const e of d.errors.slice(0, limit)) console.log(`  ERROR   ${e}`);
  if (d.errors.length > limit) console.log(`  ... ${d.errors.length - limit} more errors (use --verbose)`);
  if (flag('warnings') || flag('verbose')) {
    for (const w of d.warnings.slice(0, limit)) console.log(`  warning ${w}`);
    if (d.warnings.length > limit) console.log(`  ... ${d.warnings.length - limit} more warnings`);
  }
}

if (problems.shipped > 0 || (flag('strict') && problems.any > 0)) {
  console.log(`\nFAILED: ${flag('strict') ? problems.any : problems.shipped} problem(s)`);
  process.exit(1);
}
console.log(`\nOK: ${rows.filter((r) => r.state === 'shipped').length} shipped catalog(s) are complete and valid.`);
