// The translation pipeline, driven by manifest.json and .state.json. It does not translate by
// itself: it works out what needs translating, writes a work file, and takes the translated lines
// back in. Re-run it whenever en.json grows or changes.
//
//   node scripts/i18n/translate.mjs status [--lang de,ja] [--wave 1]
//       per language: translated, missing, changed since last time, obsolete
//   node scripts/i18n/translate.mjs prepare [--lang de,ja] [--wave 1] [--chunk 250]
//       writes work/<code>.todo.txt (or .todo.1.txt, .todo.2.txt ... with --chunk): the keys that are
//       missing or whose English changed, with the English, the old translation when there is one,
//       and the language's glossary. Translate them.
//   (write work/<code>.done.txt: one line per key, "key translation"; a value that needs exact
//    spaces or quotes may be written as a JSON string. Several .done*.txt files are all read.)
//   node scripts/i18n/translate.mjs apply [--lang de,ja]
//       checks each line (placeholders, plurals, names) and merges the good ones into the catalog,
//       recording the sha of the English it answers; bad lines stay in the done file with the reason
//   node scripts/i18n/translate.mjs publish [--lang de,ja | --wave 1 | --all]
//       moves a complete, valid catalog from scripts/i18n/wip to src/lib/core/i18n (it is then shipped)
//   node scripts/i18n/translate.mjs sync [--lang de]
//       drops obsolete keys and puts keys back in English order
//   node scripts/i18n/translate.mjs adopt --lang de
//       records the current English sha for every key present (after hand edits)
import fs from 'node:fs';
import path from 'node:path';
import {
  checkMessage, glossaryDir, loadEnglish, loadManifest, loadState, locateCatalog, pad, readJson, saveState, sha,
  shippedCodes, shippedPath, typography, wipPath, workDir, writeCatalog,
} from './lib.mjs';

const [command = 'status', ...rest] = process.argv.slice(2);
const option = (name) => {
  const i = rest.indexOf(`--${name}`);
  return i >= 0 ? rest[i + 1] : undefined;
};
const flag = (name) => rest.includes(`--${name}`);

const manifest = loadManifest();
const english = loadEnglish();
const englishKeys = Object.keys(english);
const state = loadState();

function selectedCodes() {
  const lang = option('lang');
  if (lang) return lang.split(',').map((s) => s.trim()).filter(Boolean);
  const wave = option('wave');
  if (wave) return manifest.languages.filter((l) => String(l.wave) === wave).map((l) => l.code);
  if (flag('all')) return manifest.languages.map((l) => l.code);
  // By default: every language that already has a catalog (shipped or wip), plus wave 1.
  const have = new Set(shippedCodes());
  for (const l of manifest.languages) if (locateCatalog(l.code).missing === undefined) have.add(l.code);
  for (const l of manifest.languages) if (l.wave === 1) have.add(l.code);
  return manifest.languages.map((l) => l.code).filter((c) => have.has(c));
}

function readCatalog(code) {
  const where = locateCatalog(code);
  return { where, catalog: where.missing ? {} : readJson(where.file) };
}

function analyse(code) {
  const { where, catalog } = readCatalog(code);
  const known = state.languages[code] ?? {};
  const missing = [];
  const changed = [];
  for (const key of englishKeys) {
    if (!(key in catalog)) missing.push(key);
    else if (known[key] !== sha(english[key])) changed.push(key);
  }
  const obsolete = Object.keys(catalog).filter((k) => !(k in english));
  return { where, catalog, missing, changed, obsolete };
}

// ---- status ----------------------------------------------------------------------------------

function status() {
  console.log(`English has ${englishKeys.length} keys.\n`);
  console.log(`${pad('language', 10)}${pad('state', 9)}${pad('translated', 12)}${pad('missing', 9)}${pad('changed', 9)}obsolete`);
  let todo = 0;
  for (const code of selectedCodes()) {
    const a = analyse(code);
    const have = englishKeys.length - a.missing.length;
    const st = a.where.missing ? 'none' : a.where.shipped ? 'shipped' : 'wip';
    console.log(`${pad(code, 10)}${pad(st, 9)}${pad(have, 12)}${pad(a.missing.length, 9)}${pad(a.changed.length, 9)}${a.obsolete.length}`);
    todo += a.missing.length + a.changed.length;
  }
  console.log(todo === 0 ? '\nNothing to translate.' : `\n${todo} key(s) to translate.`);
}

// ---- the line format of work files ---------------------------------------------------------------

/** A value as one line: plain, unless it has spaces at an end or starts and ends with a quote. */
function asLine(value) {
  return value !== value.trim() || (value.startsWith('"') && value.endsWith('"')) ? JSON.stringify(value) : value;
}

function fromLine(text) {
  const t = text.trim();
  if (t.length >= 2 && t.startsWith('"') && t.endsWith('"')) {
    try {
      return JSON.parse(t);
    } catch {
      /* not a JSON string after all: take it as written */
    }
  }
  return t;
}

function glossaryFor(code) {
  const file = path.join(glossaryDir, `${code}.json`);
  return fs.existsSync(file) ? readJson(file) : undefined;
}

// ---- prepare ---------------------------------------------------------------------------------

function prepare() {
  fs.mkdirSync(workDir, { recursive: true });
  const size = Number(option('chunk') ?? 0);
  for (const code of selectedCodes()) {
    const a = analyse(code);
    const keys = englishKeys.filter((k) => a.missing.includes(k) || a.changed.includes(k));
    for (const f of fs.readdirSync(workDir)) if (f.startsWith(`${code}.todo`)) fs.unlinkSync(path.join(workDir, f));
    if (keys.length === 0) {
      console.log(`${code}: nothing to translate`);
      continue;
    }
    const glossary = glossaryFor(code);
    const head = [
      `# ${code}: ${keys.length} to translate (${a.missing.length} missing, ${a.changed.length} changed)`,
      '# Write the answers in work/' + code + '.done.txt, one per line:  key translation',
      '# Keep {placeholders} and plural/select structure; keep product names; "Marquee" stays Marquee.',
    ];
    if (glossary) {
      head.push('# Glossary:');
      for (const [term, entry] of Object.entries(glossary.terms ?? {})) head.push(`#   ${term} = ${entry.use}`);
      for (const note of glossary.notes ?? []) head.push(`#   note: ${note}`);
    }
    const lines = [];
    for (const key of keys) {
      lines.push(`${key} ${asLine(english[key])}`);
      if (a.changed.includes(key) && a.catalog[key] !== undefined) lines.push(`#   was: ${a.catalog[key]}`);
    }
    const parts = size > 0 ? Math.ceil(keys.length / size) : 1;
    if (parts === 1) {
      const file = path.join(workDir, `${code}.todo.txt`);
      fs.writeFileSync(file, `${head.join('\n')}\n\n${lines.join('\n')}\n`);
      console.log(`${code}: ${keys.length} key(s) -> ${path.relative(process.cwd(), file)}`);
    } else {
      // Split on key boundaries, keeping a "was:" comment with its key.
      let n = 0;
      let bucket = [];
      let count = 0;
      const flush = () => {
        n += 1;
        const file = path.join(workDir, `${code}.todo.${n}.txt`);
        fs.writeFileSync(file, `${head.join('\n')}\n# part ${n} of ${parts}\n\n${bucket.join('\n')}\n`);
        console.log(`${code}: part ${n}/${parts} -> ${path.relative(process.cwd(), file)}`);
        bucket = [];
        count = 0;
      };
      for (const line of lines) {
        if (!line.startsWith('#') && count === size) flush();
        bucket.push(line);
        if (!line.startsWith('#')) count += 1;
      }
      if (bucket.length) flush();
    }
  }
}

// ---- apply -----------------------------------------------------------------------------------

function parseDone(text) {
  const entries = [];
  const bad = [];
  for (const raw of text.split(/\r?\n/)) {
    if (raw.trim() === '' || raw.startsWith('#')) continue;
    const m = /^(\S+)[ \t]+([\s\S]*)$/.exec(raw);
    if (!m) bad.push({ line: raw, why: 'expected "key translation"' });
    else entries.push({ key: m[1], value: fromLine(m[2]), line: raw });
  }
  return { entries, bad };
}

function apply() {
  fs.mkdirSync(workDir, { recursive: true });
  for (const code of selectedCodes()) {
    const files = fs.readdirSync(workDir).filter((f) => f.startsWith(`${code}.done`) && f.endsWith('.txt') && !f.includes('.applied'));
    if (files.length === 0) continue;
    const { where, catalog } = readCatalog(code);
    const known = (state.languages[code] ??= {});
    let accepted = 0;
    const rejected = [];
    for (const f of files) {
      const full = path.join(workDir, f);
      const { entries, bad } = parseDone(fs.readFileSync(full, 'utf8'));
      const keep = bad.map((b) => `# REJECTED (${b.why})\n${b.line}`);
      for (const { key, value: typed, line } of entries) {
        const value = typography(code, typed);
        if (!(key in english)) {
          keep.push(`# REJECTED (not a key in en.json)\n${line}`);
          continue;
        }
        const result = checkMessage(code, key, english[key], value);
        if (result.errors.length) {
          keep.push(`# REJECTED (${result.errors.join('; ')})\n${key} ${asLine(value)}`);
          continue;
        }
        catalog[key] = value;
        known[key] = sha(english[key]);
        accepted += 1;
      }
      if (keep.length === 0) fs.unlinkSync(full);
      else {
        fs.writeFileSync(full, `${keep.join('\n')}\n`);
        rejected.push(...keep.filter((k) => k.startsWith('# REJECTED')));
      }
    }
    // Drop what English no longer has; write in English order.
    const ordered = {};
    for (const key of englishKeys) if (key in catalog) ordered[key] = catalog[key];
    for (const key of Object.keys(known)) if (!(key in english)) delete known[key];
    writeCatalog(where.file, ordered);
    console.log(`${code}: ${accepted} accepted, ${rejected.length} rejected${rejected.length ? ' (left in the done file with the reason)' : ''}`);
    for (const r of rejected.slice(0, 8)) console.log(`   ${r.replace(/\n/g, ' | ')}`);
  }
  saveState(state);
}

// ---- publish, sync, adopt --------------------------------------------------------------------

function publish() {
  for (const code of selectedCodes()) {
    const where = locateCatalog(code);
    if (where.missing) {
      console.log(`${code}: no catalog to publish`);
      continue;
    }
    if (where.shipped) {
      console.log(`${code}: already shipped`);
      continue;
    }
    const catalog = readJson(where.file);
    const problems = [];
    for (const key of englishKeys) {
      if (!(key in catalog)) problems.push(`${key}: missing`);
      else for (const e of checkMessage(code, key, english[key], catalog[key]).errors) problems.push(`${key}: ${e}`);
    }
    for (const key of Object.keys(catalog)) if (!(key in english)) problems.push(`${key}: obsolete`);
    if (problems.length) {
      console.log(`${code}: NOT published, ${problems.length} problem(s), first: ${problems.slice(0, 3).join(' | ')}`);
      continue;
    }
    writeCatalog(shippedPath(code), catalog);
    fs.unlinkSync(where.file);
    console.log(`${code}: published (${Object.keys(catalog).length} keys)`);
  }
}

function sync() {
  for (const code of selectedCodes()) {
    const { where, catalog } = readCatalog(code);
    if (where.missing) continue;
    const ordered = {};
    for (const key of englishKeys) if (key in catalog) ordered[key] = catalog[key];
    const known = state.languages[code] ?? {};
    for (const key of Object.keys(known)) if (!(key in english)) delete known[key];
    writeCatalog(where.file, ordered);
    console.log(`${code}: ${Object.keys(catalog).length - Object.keys(ordered).length} obsolete key(s) dropped`);
  }
  saveState(state);
}

function adopt() {
  for (const code of selectedCodes()) {
    const { where, catalog } = readCatalog(code);
    if (where.missing) continue;
    const known = (state.languages[code] ??= {});
    for (const key of Object.keys(catalog)) if (key in english) known[key] = sha(english[key]);
    console.log(`${code}: recorded ${Object.keys(known).length} key(s)`);
  }
  saveState(state);
}

const commands = { status, prepare, apply, publish, sync, adopt };
if (!commands[command]) {
  console.error(`unknown command "${command}"; use one of: ${Object.keys(commands).join(', ')}`);
  process.exit(2);
}
commands[command]();
void wipPath;
