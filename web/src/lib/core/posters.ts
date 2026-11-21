import type { PosterItem } from './types';

/** Titles that have a colour token in tokens.css (--poster-<slug>). */
const POSTER_SLUGS = [
  'metropolis',
  'nosferatu',
  'the-general',
  'sherlock-jr',
  'safety-last',
  'the-kid',
  'night-of-the-living-dead',
  'his-girl-friday',
  'charade',
  'detour',
  'sintel',
  'big-buck-bunny',
  'tears-of-steel',
  'spring',
  'cosmos-laundromat',
  'pioneer-one',
  'undersea-kingdom',
  'the-phantom-creeps',
  'sprite-fright',
  'elephants-dream',
  'plan-9-from-outer-space',
] as const;

export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

/** CSS colour for a title's poster: its own token, or a stable pick for unknown titles. */
export function posterColor(title: string): string {
  const slug = slugify(title);
  if ((POSTER_SLUGS as readonly string[]).includes(slug)) return `var(--poster-${slug})`;
  let hash = 0;
  for (let i = 0; i < slug.length; i++) hash = (hash * 31 + slug.charCodeAt(i)) >>> 0;
  return `var(--poster-${POSTER_SLUGS[hash % POSTER_SLUGS.length]})`;
}

/**
 * Port of titleSize() from the Cinema mockups: the serif title shrinks with its length and
 * with its longest word so it fits the poster. `avail` is the usable width in px and `floor`
 * the smallest size allowed (Home: 124 and 20).
 */
export function titleSize(title: string, avail = 124, floor = 20): number {
  const words = title.split(' ');
  let longest = 0;
  for (const word of words) {
    if (word.length > longest) longest = word.length;
  }
  const len = title.length;
  let capLen = 32;
  if (len >= 20) capLen = 22;
  else if (len >= 14) capLen = 24;
  else if (len >= 11) capLen = 27;
  else if (len >= 8) capLen = 30;
  const capWord = Math.floor(avail / (longest * 0.52));
  let size = Math.min(capLen, capWord);
  if (size < floor) size = floor;
  return size;
}

export type PosterSize = 'md' | 'sm' | 'mini';

/**
 * Title size for each poster size.
 * md: Home grid, 150 x 188. sm: phone rows, 108 x 132 (capped at 24, as in the phone mockup).
 * mini: 96 x 120 in Requests, the md size scaled to about 0.62.
 */
export function posterTitleSize(title: string, size: PosterSize): number {
  if (size === 'sm') return Math.min(titleSize(title, 84, 16), 24);
  if (size === 'mini') return Math.max(14, Math.round(titleSize(title) * 0.62));
  return titleSize(title);
}

/** Mock stage: every id shows the same title, so every poster links to id 1 of its kind. */
const MOCK_ID = '1';

/** Detail page for a poster item. */
export function titleHref(item: Pick<PosterItem, 'kind' | 'id'>): string {
  if (item.kind === 'tv') return `/tv/${MOCK_ID}`;
  if (item.kind === 'artist') return `/music/artists/${MOCK_ID}`;
  return `/movies/${MOCK_ID}`;
}
