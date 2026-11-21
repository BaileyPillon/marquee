// Who is using Marquee right now. One shared object for the whole app (Svelte 5 runes).
//
// On start it asks the server two things: does this install still need its owner (first run), and
// who am I (`/me`). First run routes to /setup, a missing session routes to /signin. After that, any
// API call that comes back 401 routes to /signin too (see setUnauthorizedHandler in api.ts).
import { get } from 'svelte/store';
import { ApiError, getJSON, postJSON, setUnauthorizedHandler } from './api';
import { current, matchRoute, navigate } from './router';

export type Role = 'owner' | 'admin' | 'member';

export interface User {
  id: number;
  name: string;
  role: Role;
}

/** The answer of `GET /me`. */
export interface Me {
  user: User;
  /** How the call was authenticated: a browser `session` or an API `key`. */
  via: string;
  scopes: string[];
}

/**
 * loading: nothing answered yet. ready: the server answered, so we know whether someone is signed
 * in. offline: the server could not be reached, so we do not know (pages show what they can).
 */
export type SessionStatus = 'loading' | 'ready' | 'offline';

/**
 * Where to go after signing in. `next` comes from the address bar, so only a plain path on this
 * site is accepted; anything else (another site, a full-screen page like /signin) means Home.
 */
export function landingPath(next: string | null | undefined): string {
  if (!next || !next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\')) return '/';
  let url: URL;
  try {
    url = new URL(next, location.origin);
  } catch {
    return '/';
  }
  if (url.origin !== location.origin) return '/';
  if (matchRoute(url.pathname, url.search).route.fullScreen) return '/';
  return url.pathname + url.search + url.hash;
}

class Session {
  status = $state<SessionStatus>('loading');
  /** The signed-in person, or null. */
  me = $state<User | null>(null);
  /** True on a fresh install: the first visitor becomes the owner. */
  needsOwner = $state(false);
  readonly role = $derived<Role | null>(this.me?.role ?? null);

  #starting: Promise<void> | null = null;

  /** Ask the server who we are and route accordingly. Safe to call again (it joins a run in flight). */
  start(): Promise<void> {
    this.#starting ??= this.#load().finally(() => {
      this.#starting = null;
    });
    return this.#starting;
  }

  async #load(): Promise<void> {
    try {
      const setup = await getJSON<{ needsOwner: boolean }>('setup');
      this.needsOwner = setup.needsOwner;
      if (setup.needsOwner) {
        this.me = null;
        this.status = 'ready';
        if (get(current).route.name !== 'setup') navigate('/setup', { replace: true });
        return;
      }
      try {
        this.me = (await getJSON<Me>('me')).user;
      } catch (err) {
        if (!(err instanceof ApiError) || err.status !== 401) throw err;
        this.me = null;
      }
    } catch {
      // Server down, or something that is not Marquee answering: carry on without an identity.
      this.status = 'offline';
      return;
    }
    this.status = 'ready';
    this.#route();
  }

  /** Move off a page that does not fit who we turned out to be. */
  #route(): void {
    const match = get(current);
    const name = match.route.name;
    if (this.me) {
      if (name === 'setup' || name === 'signin') {
        navigate(landingPath(match.query.get('next')), { replace: true });
      }
    } else if (name === 'setup') {
      navigate('/signin', { replace: true });
    } else if (!match.route.fullScreen) {
      this.expire();
    }
  }

  /** Someone just signed in, set up the owner, or accepted an invite. */
  adopt(user: User): void {
    this.me = user;
    this.needsOwner = false;
    this.status = 'ready';
  }

  /** The session is gone: forget the person and go to sign-in, remembering where they were. */
  expire(): void {
    this.me = null;
    const match = get(current);
    if (match.route.fullScreen) return;
    const search = match.query.toString();
    const here = match.path + (search ? `?${search}` : '');
    navigate(here === '/' ? '/signin' : `/signin?next=${encodeURIComponent(here)}`, {
      replace: true,
    });
  }

  /** End the session. Resolves false when the server could not be told (the person stays signed in). */
  async signOut(): Promise<boolean> {
    try {
      await postJSON('auth/logout');
    } catch (err) {
      // A 401 means the session was already over, which is what we wanted.
      if (!(err instanceof ApiError) || err.status !== 401) return false;
    }
    this.me = null;
    navigate('/signin', { replace: true });
    return true;
  }
}

export const session = new Session();

setUnauthorizedHandler(() => session.expire());
