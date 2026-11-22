// Self-check for the message parser the browser and validate.mjs share.
//   node --test scripts/i18n/icu.test.mjs      (from web/)
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { format, parse, shapeOf, IcuSyntaxError } from '../../src/lib/core/icu.ts';

const ctx = (lang) => {
  const rules = new Intl.PluralRules(lang);
  const numbers = new Intl.NumberFormat(lang);
  return { plural: (n) => rules.select(n), number: (n) => numbers.format(n) };
};

test('plain text and values', () => {
  assert.equal(format("Couldn't save, {name}.", { name: 'Ann' }, ctx('en')), "Couldn't save, Ann.");
  assert.equal(format('{a} of {b}', { a: 1, b: 1234 }, ctx('en')), '1 of 1,234');
  assert.equal(format('Hello {who}', {}, ctx('en')), 'Hello {who}');
  assert.equal(format('# stays literal outside a plural', {}, ctx('en')), '# stays literal outside a plural');
});

test('plural, English', () => {
  const m = '{n, plural, one {# track} other {# tracks}}';
  assert.equal(format(m, { n: 1 }, ctx('en')), '1 track');
  assert.equal(format(m, { n: 0 }, ctx('en')), '0 tracks');
  assert.equal(format(m, { n: 12345 }, ctx('en')), '12,345 tracks');
});

test('plural, other languages use their own categories', () => {
  const pl = '{n, plural, one {# plik} few {# pliki} many {# plików} other {# pliku}}';
  assert.equal(format(pl, { n: 1 }, ctx('pl')), '1 plik');
  assert.equal(format(pl, { n: 3 }, ctx('pl')), '3 pliki');
  assert.equal(format(pl, { n: 5 }, ctx('pl')), '5 plików');
  assert.equal(format(pl, { n: 22 }, ctx('pl')), '22 pliki');
  const ar = '{n, plural, zero {لا ملفات} one {ملف واحد} two {ملفان} few {# ملفات} many {# ملفًا} other {# ملف}}';
  assert.equal(format(ar, { n: 0 }, ctx('ar')), 'لا ملفات');
  assert.equal(format(ar, { n: 2 }, ctx('ar')), 'ملفان');
  const ja = '{n, plural, other {#件}}';
  assert.equal(format(ja, { n: 1 }, ctx('ja')), '1件');
});

test('exact matches, offset and nesting', () => {
  const m = '{n, plural, =0 {none} one {# item} other {# items}}';
  assert.equal(format(m, { n: 0 }, ctx('en')), 'none');
  assert.equal(format(m, { n: 1 }, ctx('en')), '1 item');
  const off = '{n, plural, offset:1 =0 {nobody} =1 {{who}} one {{who} and # other} other {{who} and # others}}';
  assert.equal(format(off, { n: 3, who: 'Ann' }, ctx('en')), 'Ann and 2 others');
  const nested =
    '{n, plural, one {Imported # source.} other {Imported # sources.}} {s, plural, one {# was skipped.} other {# were skipped.}}';
  assert.equal(format(nested, { n: 1, s: 2 }, ctx('en')), 'Imported 1 source. 2 were skipped.');
  const inner = '{n, plural, one {{names} is linked.} other {{names} are linked.}}';
  assert.equal(format(inner, { n: 2, names: 'A and B' }, ctx('en')), 'A and B are linked.');
});

test('select', () => {
  const m = '{kind, select, movie {Movie} show {Show} other {Title}}';
  assert.equal(format(m, { kind: 'show' }, ctx('en')), 'Show');
  assert.equal(format(m, { kind: 'artist' }, ctx('en')), 'Title');
});

test('a plural with a missing or non-numeric value falls back to other', () => {
  const m = '{n, plural, one {# file} other {# files}}';
  assert.equal(format(m, {}, ctx('en')), '# files');
  assert.equal(format(m, { n: 'x' }, ctx('en')), '# files');
});

test('broken messages throw', () => {
  for (const bad of [
    '{n',
    '{n, plural, one {x}}',
    '{n, plural, one {x} other {y}',
    'a } b',
    '{n, date}',
    '{}',
    '{n, select, a {x} a {y} other {z}}',
  ]) {
    assert.throws(() => parse(bad), IcuSyntaxError, bad);
  }
});

test('shapeOf lists names and blocks', () => {
  const shape = shapeOf('{n, plural, one {{who} has # file} other {{who} has # files}} in {where}');
  assert.deepEqual([...shape.names].sort(), ['n', 'where', 'who']);
  assert.deepEqual(shape.blocks, [{ name: 'n', kind: 'plural', cases: ['one', 'other'] }]);
});
