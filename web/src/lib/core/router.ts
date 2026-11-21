// A tiny history-API router. No dependencies beyond svelte/store.
//
// The Go server answers every non-API path with index.html, so these are real paths, not hashes.
import { writable, type Readable } from 'svelte/store';
import type { MessageKey } from './i18n';

/** Which rail item (or phone tab) a route belongs to. */
export type Section =
  | 'home'
  | 'explore'
  | 'popular'
  | 'movies'
  | 'tv'
  | 'music'
  | 'requests'
  | 'downloads'
  | 'calendar'
  | 'settings';

export type RouteName =
  | 'home'
  | 'explore'
  | 'popular'
  | 'movies'
  | 'movie'
  | 'tv'
  | 'show'
  | 'music'
  | 'artist'
  | 'requests'
  | 'downloads'
  | 'calendar'
  | 'settings'
  | 'notFound';

export interface RouteDef {
  name: RouteName;
  /** Path pattern; `:name` captures one segment. */
  path: string;
  /** Page title shown in the tab and the stub heading. */
  title: MessageKey;
  /** Highlighted rail item; null for the not-found page. */
  section: Section | null;
  /** Desktop search bar and "Ask for…" above the page. */
  topBar: boolean;
}

export const routes: readonly RouteDef[] = [
  { name: 'home', path: '/', title: 'route.home', section: 'home', topBar: true },
  { name: 'explore', path: '/explore', title: 'route.explore', section: 'explore', topBar: true },
  { name: 'popular', path: '/popular', title: 'route.popular', section: 'popular', topBar: true },
  { name: 'movies', path: '/movies', title: 'route.movies', section: 'movies', topBar: true },
  { name: 'movie', path: '/movies/:id', title: 'route.movie', section: 'movies', topBar: false },
  { name: 'tv', path: '/tv', title: 'route.tv', section: 'tv', topBar: true },
  { name: 'show', path: '/tv/:id', title: 'route.show', section: 'tv', topBar: false },
  { name: 'music', path: '/music', title: 'route.music', section: 'music', topBar: true },
  {
    name: 'artist',
    path: '/music/artists/:id',
    title: 'route.artist',
    section: 'music',
    topBar: false,
  },
  {
    name: 'requests',
    path: '/requests',
    title: 'route.requests',
    section: 'requests',
    topBar: false,
  },
  {
    name: 'downloads',
    path: '/downloads',
    title: 'route.downloads',
    section: 'downloads',
    topBar: true,
  },
  {
    name: 'calendar',
    path: '/calendar',
    title: 'route.calendar',
    section: 'calendar',
    topBar: false,
  },
  {
    name: 'settings',
    path: '/settings',
    title: 'route.settings',
    section: 'settings',
    topBar: false,
  },
];

const notFoundRoute: RouteDef = {
  name: 'notFound',
  path: '*',
  title: 'notFound.title',
  section: null,
  topBar: false,
};

export interface Match {
  route: RouteDef;
  params: Record<string, string>;
  /** Normalised pathname (no trailing slash except "/"). */
  path: string;
  query: URLSearchParams;
}

function normalise(pathname: string): string {
  const trimmed = pathname.replace(/\/+$/, '');
  return trimmed === '' ? '/' : trimmed;
}

function safeDecode(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

function matchPattern(pattern: string, path: string): Record<string, string> | null {
  const want = pattern.split('/').filter(Boolean);
  const have = path.split('/').filter(Boolean);
  if (want.length !== have.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < want.length; i++) {
    const w = want[i] as string;
    const h = have[i] as string;
    if (w.startsWith(':')) params[w.slice(1)] = safeDecode(h);
    else if (w !== h) return null;
  }
  return params;
}

/** Resolve a path (and optional query string) to a route. Pure, so it is easy to test. */
export function matchRoute(pathname: string, search = ''): Match {
  const path = normalise(pathname);
  const query = new URLSearchParams(search);
  for (const route of routes) {
    const params = matchPattern(route.path, path);
    if (params) return { route, params, path, query };
  }
  return { route: notFoundRoute, params: {}, path, query };
}

function fromLocation(): Match {
  return matchRoute(location.pathname, location.search);
}

const store = writable<Match>(fromLocation());

/** The current route. Use `$current` in components. */
export const current: Readable<Match> = { subscribe: store.subscribe };

/** Go to a same-origin path without a page load. */
export function navigate(to: string, options: { replace?: boolean } = {}): void {
  const url = new URL(to, location.href);
  if (url.origin !== location.origin) {
    location.assign(url.href);
    return;
  }
  const next = url.pathname + url.search + url.hash;
  if (next === location.pathname + location.search + location.hash) return;
  if (options.replace) history.replaceState(null, '', next);
  else history.pushState(null, '', next);
  store.set(matchRoute(url.pathname, url.search));
}

function onClick(event: MouseEvent): void {
  if (event.defaultPrevented || event.button !== 0) return;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const anchor = target.closest('a');
  if (!anchor || !anchor.hasAttribute('href')) return;
  if (anchor.target && anchor.target !== '_self') return;
  if (anchor.hasAttribute('download') || anchor.relList.contains('external')) return;
  if (anchor.origin !== location.origin) return;
  // Same-page fragment links (skip links) keep their native behaviour.
  if (anchor.pathname === location.pathname && anchor.search === location.search && anchor.hash) {
    return;
  }
  event.preventDefault();
  navigate(anchor.pathname + anchor.search + anchor.hash);
}

function onPopState(): void {
  store.set(fromLocation());
}

/** Start handling link clicks and the back/forward buttons. Returns a cleanup function. */
export function startRouter(): () => void {
  document.addEventListener('click', onClick);
  window.addEventListener('popstate', onPopState);
  store.set(fromLocation());
  return () => {
    document.removeEventListener('click', onClick);
    window.removeEventListener('popstate', onPopState);
  };
}
