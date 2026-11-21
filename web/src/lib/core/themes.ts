// Panel colours for a title's hero. The catalog has no colours of its own, so each title gets one of
// these sets by a stable pick from its name, the way posters pick their colour (posters.ts). The
// first three are the Cinema canvas's own (Metropolis, Pioneer One, Chopin); text on every one of
// them is the light ink of the page, well past 4.5:1.
import type { ApiKind } from './catalog';
import type { HeroTheme } from './types';

const THEMES: readonly HeroTheme[] = [
  { bg: '#16213A', artBg: '#0F1624', disc: '#2A3C63', towerA: '#1B2842', towerB: '#0A0F18' },
  { bg: '#241B2A', artBg: '#2A2030', disc: '#3B2A3C', towerA: '#2A2030', towerB: '#0E0B10' },
  { bg: '#2B2142', artBg: '#1B1329', disc: '#3F3160', towerA: '#2B2142', towerB: '#120C1D' },
  { bg: '#142A2B', artBg: '#0D1C1D', disc: '#25494A', towerA: '#183536', towerB: '#081213' },
  { bg: '#2E1A1D', artBg: '#1E0F11', disc: '#4A2A2F', towerA: '#341F22', towerB: '#130A0B' },
  { bg: '#252316', artBg: '#191810', disc: '#3F3B22', towerA: '#2D2A19', towerB: '#0F0E08' },
];

/** The movie sets: a show and an artist have the one their drawn art was made for (dusk, keys). */
const MOVIE_SETS = [0, 3, 4, 5] as const;

/** The hero colours for a title: the same name always gets the same set. */
export function heroTheme(name: string, kind: ApiKind = 'movie'): HeroTheme {
  if (kind === 'show') return THEMES[1] as HeroTheme;
  if (kind === 'artist') return THEMES[2] as HeroTheme;
  let hash = 0;
  for (let i = 0; i < name.length; i++) hash = (hash * 31 + name.charCodeAt(i)) >>> 0;
  return THEMES[MOVIE_SETS[hash % MOVIE_SETS.length] as number] as HeroTheme;
}
