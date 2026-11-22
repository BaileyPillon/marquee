// Checks that the code and en.json agree.
//   - every t('key') in src exists in en.json                          (error)
//   - t('key', { ... }) passes the placeholders the English message uses (error when one is missing)
//   - a key-like string that is not in en.json but sits in a namespace that is  (warning: a typo?)
//   - keys nothing refers to                                           (listed; not an error)
// A key counts as used when its name appears as a string literal anywhere in src (nav tables, error
// maps and so on), or matches the fixed start of a template such as `role.${name}`.
//
//   node scripts/i18n/extract-check.mjs [--strict-unused] [--quiet]
import fs from 'node:fs';
import path from 'node:path';
import { loadEnglish, placeholderNames, srcDir } from './lib.mjs';

const strictUnused = process.argv.includes('--strict-unused');
const quiet = process.argv.includes('--quiet');
const english = loadEnglish();
const keys = new Set(Object.keys(english));
const namespaces = new Set([...keys].map((k) => k.split('.')[0]));

function walk(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...walk(full));
    else if (/\.(ts|svelte)$/.test(entry.name) && !entry.name.endsWith('.d.ts')) out.push(full);
  }
  return out;
}

/** The text of the call's arguments, from just after "(" to the matching ")", skipping strings. */
function callArguments(text, open) {
  let depth = 0;
  for (let i = open; i < text.length; i += 1) {
    const c = text[i];
    if (c === "'" || c === '"' || c === '`') {
      const quote = c;
      i += 1;
      while (i < text.length && text[i] !== quote) {
        if (text[i] === '\\') i += 1;
        else if (quote === '`' && text[i] === '$' && text[i + 1] === '{') {
          // skip the ${ ... } of a template, which may hold quotes of its own
          let d = 0;
          i += 1;
          for (; i < text.length; i += 1) {
            if (text[i] === '{') d += 1;
            else if (text[i] === '}' && --d === 0) break;
          }
        }
        i += 1;
      }
    } else if (c === '(' || c === '{' || c === '[') depth += 1;
    else if (c === ')' || c === '}' || c === ']') {
      depth -= 1;
      if (depth === 0) return text.slice(open + 1, i);
    }
  }
  return text.slice(open + 1);
}

/** Split on commas that are not inside brackets or strings. */
function splitTop(text) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < text.length; i += 1) {
    const c = text[i];
    if (c === "'" || c === '"' || c === '`') {
      const quote = c;
      i += 1;
      while (i < text.length && text[i] !== quote) {
        if (text[i] === '\\') i += 1;
        else if (quote === '`' && text[i] === '$' && text[i + 1] === '{') {
          let d = 0;
          i += 1;
          for (; i < text.length; i += 1) {
            if (text[i] === '{') d += 1;
            else if (text[i] === '}' && --d === 0) break;
          }
        }
        i += 1;
      }
    } else if ('({['.includes(c)) depth += 1;
    else if (')}]'.includes(c)) depth -= 1;
    else if (c === ',' && depth === 0) {
      parts.push(text.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(text.slice(start));
  return parts.map((p) => p.trim()).filter(Boolean);
}

/** The names an object literal passes: `{ n, who: x, ...rest }`. undefined when it cannot be known. */
function passedNames(arg) {
  if (!arg.startsWith('{') || !arg.endsWith('}')) return undefined;
  const names = new Set();
  for (const entry of splitTop(arg.slice(1, -1))) {
    if (entry.startsWith('...')) return undefined;
    const m = /^(?:\[?['"]?)([A-Za-z_$][\w$]*)/.exec(entry);
    if (!m) return undefined;
    names.add(m[1]);
  }
  return names;
}

const used = new Set();
const prefixes = new Map(); // template prefix -> where
const missing = [];
const paramProblems = [];
const suspects = [];

for (const file of walk(srcDir)) {
  const text = fs.readFileSync(file, 'utf8');
  const rel = path.relative(srcDir, file).replace(/\\/g, '/');
  const lineOf = (index) => text.slice(0, index).split('\n').length;

  // t('key', {...}) with a literal key.
  const callRe = /(?<![\w.$])t\(\s*(['"`])((?:\\.|(?!\1)[^\\])*)\1/g;
  for (let m; (m = callRe.exec(text)); ) {
    const [whole, quote, raw] = m;
    const open = m.index + whole.indexOf('(');
    if (quote === '`' && raw.includes('${')) continue; // dynamic: handled below
    const key = raw;
    if (!keys.has(key)) {
      missing.push(`${rel}:${lineOf(m.index)}  t('${key}') is not in en.json`);
      continue;
    }
    used.add(key);
    const argv = splitTop(callArguments(text, open));
    const needed = placeholderNames(english[key]);
    if (needed.size === 0) {
      if (argv[1] && /^\{[^}]+\}$/.test(argv[1]) && passedNames(argv[1])?.size) {
        paramProblems.push(`${rel}:${lineOf(m.index)}  t('${key}') passes values the message does not use: ${[...passedNames(argv[1])].join(', ')}`);
      }
      continue;
    }
    const given = argv[1] ? passedNames(argv[1]) : new Set();
    if (given === undefined) continue; // a spread or a variable: cannot tell
    const absent = [...needed].filter((n) => !given.has(n));
    const unused = [...given].filter((n) => !needed.has(n));
    if (absent.length) paramProblems.push(`${rel}:${lineOf(m.index)}  t('${key}') does not pass {${absent.join('}, {')}}`);
    if (unused.length) paramProblems.push(`${rel}:${lineOf(m.index)}  t('${key}') passes ${unused.join(', ')}, which the message does not use`);
  }

  // Any string literal that is exactly a key.
  const strRe = /(['"])([a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9_]+)+)\1/g;
  for (let m; (m = strRe.exec(text)); ) {
    const s = m[2];
    if (keys.has(s)) used.add(s);
    else if (namespaces.has(s.split('.')[0]) && !/\.(ts|svelte|json|css|js|svg)$/.test(s)) {
      suspects.push(`${rel}:${lineOf(m.index)}  '${s}' looks like a key but is not in en.json`);
    }
  }

  // Templates that build a key: `role.${x}` or 'alerts.event.' + x
  const tplRe = /`([a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9_]+)*\.)\$\{/g;
  for (let m; (m = tplRe.exec(text)); ) prefixes.set(m[1], `${rel}:${lineOf(m.index)}`);
  const catRe = /(['"])([a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9_]+)*\.)\1\s*\+/g;
  for (let m; (m = catRe.exec(text)); ) prefixes.set(m[2], `${rel}:${lineOf(m.index)}`);
}

for (const [prefix, where] of prefixes) {
  const hits = [...keys].filter((k) => k.startsWith(prefix));
  if (hits.length === 0) {
    if (namespaces.has(prefix.split('.')[0])) missing.push(`${where}  \`${prefix}\${...}\` matches no key in en.json`);
    continue;
  }
  for (const k of hits) used.add(k);
}

const unusedKeys = [...keys].filter((k) => !used.has(k));
const unique = (list) => [...new Set(list)];

if (!quiet) {
  console.log(`${keys.size} keys in en.json, ${used.size} used in src.`);
}
if (missing.length) {
  console.log(`\nMISSING from en.json (${missing.length}):`);
  for (const line of unique(missing)) console.log(`  ${line}`);
}
if (paramProblems.length) {
  console.log(`\nPLACEHOLDER mismatches (${paramProblems.length}):`);
  for (const line of unique(paramProblems)) console.log(`  ${line}`);
}
if (suspects.length && !quiet) {
  console.log(`\nLook like keys but are not (${suspects.length}):`);
  for (const line of unique(suspects)) console.log(`  ${line}`);
}
if (unusedKeys.length) {
  console.log(`\nUNUSED keys (${unusedKeys.length}) - nothing in src refers to them:`);
  for (const k of unusedKeys) console.log(`  ${k}`);
}

const failed = missing.length > 0 || paramProblems.length > 0 || (strictUnused && unusedKeys.length > 0);
console.log(failed ? '\nFAILED' : '\nOK');
process.exit(failed ? 1 : 0);
