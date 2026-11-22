// A small ICU-like message format. It understands only what the catalogs use:
//
//   {name}                                   a value
//   {n, plural, one {# file} other {# files}}  plural by the language's rules; `=0 {..}` exact; # is the number
//   {kind, select, movie {..} other {..}}     choose by a word
//
// Anything else (dates, numbers, skeletons) goes through the Intl helpers in i18n.ts instead. There
// is no apostrophe escaping: a quote is a quote, and the only characters with meaning are { } and
// (inside a plural branch) #.
//
// This file imports nothing, so the catalog validator (web/scripts/i18n/validate.mjs) runs the very
// same parser the browser does.

export type PluralCategory = 'zero' | 'one' | 'two' | 'few' | 'many' | 'other';

export type Node =
  | string
  | { type: 'arg'; name: string }
  | { type: 'pound' }
  | { type: 'plural'; name: string; offset: number; cases: Record<string, Node[]> }
  | { type: 'select'; name: string; cases: Record<string, Node[]> };

export class IcuSyntaxError extends Error {
  readonly source: string;
  readonly at: number;
  constructor(message: string, source: string, at: number) {
    super(`${message} (at ${at} in "${source}")`);
    this.name = 'IcuSyntaxError';
    this.source = source;
    this.at = at;
  }
}

export interface FormatContext {
  /** The plural category of a number in the message's own language. */
  plural(n: number): PluralCategory;
  /** A number in the reader's digits and grouping. */
  number(n: number): string;
}

const NAME = /[A-Za-z0-9_]/;
const parsed = new Map<string, Node[]>();

/** Turn a message into nodes. Throws IcuSyntaxError on a broken message. */
export function parse(source: string): Node[] {
  let i = 0;

  const fail = (message: string): never => {
    throw new IcuSyntaxError(message, source, i);
  };
  const skipSpace = (): void => {
    while (i < source.length && /\s/.test(source[i] as string)) i += 1;
  };
  const readWord = (): string => {
    const start = i;
    while (i < source.length && NAME.test(source[i] as string)) i += 1;
    return source.slice(start, i);
  };

  function nodes(inPlural: boolean, depth: number): Node[] {
    const out: Node[] = [];
    let text = '';
    const flush = (): void => {
      if (text) out.push(text);
      text = '';
    };
    while (i < source.length) {
      const c = source[i] as string;
      if (c === '{') {
        flush();
        i += 1;
        out.push(argument(inPlural, depth));
      } else if (c === '}') {
        if (depth === 0) fail('Unmatched }');
        break;
      } else if (c === '#' && inPlural) {
        flush();
        out.push({ type: 'pound' });
        i += 1;
      } else {
        text += c;
        i += 1;
      }
    }
    flush();
    return out;
  }

  function argument(inPlural: boolean, depth: number): Node {
    skipSpace();
    const name = readWord();
    if (!name) fail('Expected a name after {');
    skipSpace();
    if (source[i] === '}') {
      i += 1;
      return { type: 'arg', name };
    }
    if (source[i] !== ',') fail(`Expected "," or "}" after {${name}`);
    i += 1;
    skipSpace();
    const kind = readWord();
    if (kind !== 'plural' && kind !== 'select') fail(`Unknown argument type "${kind}" (only plural and select)`);
    skipSpace();
    if (source[i] !== ',') fail(`Expected "," after {${name}, ${kind}`);
    i += 1;

    let offset = 0;
    skipSpace();
    if (kind === 'plural' && source.startsWith('offset:', i)) {
      i += 'offset:'.length;
      skipSpace();
      const digits = readWord();
      if (!/^\d+$/.test(digits)) fail('offset: needs a whole number');
      offset = Number(digits);
    }

    const cases: Record<string, Node[]> = {};
    for (;;) {
      skipSpace();
      if (source[i] === '}') {
        i += 1;
        break;
      }
      if (i >= source.length) fail(`Unclosed {${name}, ${kind}`);
      const start = i;
      while (i < source.length && !/[\s{}]/.test(source[i] as string)) i += 1;
      const selector = source.slice(start, i);
      if (!selector) fail('Expected a case name');
      if (selector in cases) fail(`Case "${selector}" appears twice`);
      skipSpace();
      if (source[i] !== '{') fail(`Expected "{" after case "${selector}"`);
      i += 1;
      cases[selector] = nodes(kind === 'plural' || inPlural, depth + 1);
      if (source[i] !== '}') fail(`Unclosed case "${selector}"`);
      i += 1;
    }
    if (!('other' in cases)) fail(`{${name}, ${kind}} needs an "other" case`);
    return kind === 'plural' ? { type: 'plural', name, offset, cases } : { type: 'select', name, cases };
  }

  const result = nodes(false, 0);
  if (i < source.length) fail('Unexpected }');
  return result;
}

/** Fill a message in. A value that is missing from `params` is left as `{name}`. */
export function format(
  source: string,
  params: Record<string, string | number> | undefined,
  ctx: FormatContext,
): string {
  // Most messages are plain text or a few {name} holes; skip the parser for the plain ones.
  if (!source.includes('{')) return source;
  let nodes = parsed.get(source);
  if (!nodes) {
    nodes = parse(source);
    parsed.set(source, nodes);
  }
  return render(nodes, params ?? {}, ctx, undefined);
}

function render(
  nodes: Node[],
  params: Record<string, string | number>,
  ctx: FormatContext,
  pound: number | undefined,
): string {
  let out = '';
  for (const node of nodes) {
    if (typeof node === 'string') {
      out += node;
    } else if (node.type === 'arg') {
      const value = params[node.name];
      out += value === undefined ? `{${node.name}}` : typeof value === 'number' ? ctx.number(value) : value;
    } else if (node.type === 'pound') {
      out += pound === undefined ? '#' : ctx.number(pound);
    } else if (node.type === 'plural') {
      const raw = params[node.name];
      const n = typeof raw === 'number' ? raw : Number(raw);
      if (raw === undefined || Number.isNaN(n)) {
        out += render(node.cases.other ?? [], params, ctx, undefined);
        continue;
      }
      const shown = n - node.offset;
      const branch = node.cases[`=${n}`] ?? node.cases[ctx.plural(shown)] ?? node.cases.other ?? [];
      out += render(branch, params, ctx, shown);
    } else {
      const raw = params[node.name];
      const branch = (raw !== undefined && node.cases[String(raw)]) || node.cases.other || [];
      out += render(branch, params, ctx, pound);
    }
  }
  return out;
}

/** One plural or select block somewhere in a message. */
export interface Block {
  name: string;
  kind: 'plural' | 'select';
  /** The case names, in order of appearance (`one`, `other`, `=0`, `movie`...). */
  cases: string[];
}

export interface Shape {
  /** Every name the message refers to, whether as a plain value or as a selector. */
  names: Set<string>;
  blocks: Block[];
}

/** What a message needs from its caller: for the validator and the key checker. */
export function shapeOf(source: string): Shape {
  const shape: Shape = { names: new Set(), blocks: [] };
  const walk = (nodes: Node[]): void => {
    for (const node of nodes) {
      if (typeof node === 'string' || node.type === 'pound') continue;
      shape.names.add(node.name);
      if (node.type === 'arg') continue;
      shape.blocks.push({ name: node.name, kind: node.type, cases: Object.keys(node.cases) });
      for (const branch of Object.values(node.cases)) walk(branch);
    }
  };
  walk(parse(source));
  return shape;
}
