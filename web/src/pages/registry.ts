// Which page component renders which route. App.svelte renders `pages[route.name] ?? Stub`, so a
// route with no entry here shows the "coming soon" stub. To add a page: import it and add one line.
//
// A page receives the current route match as the `match` prop; pages that do not need it ignore it.
import type { Component } from 'svelte';
import type { RouteName } from '../lib/core/router';
import Artist from './Artist.svelte';
import Downloads from './Downloads.svelte';
import Explore from './Explore.svelte';
import Home from './Home.svelte';
import Invite from './Invite.svelte';
import Library from './Library.svelte';
import Requests from './Requests.svelte';
import Settings from './Settings.svelte';
import Setup from './Setup.svelte';
import Show from './Show.svelte';
import SignIn from './SignIn.svelte';
import Title from './Title.svelte';

// Pages take different props (a few take none), so the registry holds them loosely typed.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type PageComponent = Component<any>;

export const pages: Partial<Record<RouteName, PageComponent>> = {
  home: Home,
  explore: Explore,
  movies: Library,
  tv: Library,
  music: Library,
  movie: Title,
  show: Show,
  artist: Artist,
  requests: Requests,
  downloads: Downloads,
  settings: Settings,
  invite: Invite,
  setup: Setup,
  signin: SignIn,
};
