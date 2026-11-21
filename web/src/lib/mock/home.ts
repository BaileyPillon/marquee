// Sample rows for Home whose backends are not built yet ("Your friends want" waits for requests,
// M4; "Coming up" waits for the calendar, M7), reproduced from the approved Cinema canvas
// (Main.dc.html). Home tags both "Preview". The featured title and "Recently added" are real: they
// come from the catalog. Strings here are content (what the server will send), not interface text.
import type { ComingEvent, PosterRowData } from '../core/types';
import { downloading, item, missing, requested } from './poster';

// A card here is sample content that points at no real title, so it leads to the requests page.
function toRequests(items: ReturnType<typeof item>[]): ReturnType<typeof item>[] {
  return items.map((it) => ({ ...it, href: '/requests' }));
}

export const friendsWant: PosterRowData = {
  id: 'friends-want',
  title: 'Your friends want',
  href: '/requests',
  items: toRequests([
    item('Metropolis', '1927', requested, 'Priya · 4K · waiting for you', 'waiting'),
    item(
      'The Phantom Creeps',
      '1939 · TV',
      requested,
      'Dee · all episodes · waiting for you',
      'waiting',
      'tv',
    ),
    item('Detour', '1945', missing, "From Marco's watchlist", 'asked'),
    item('Sintel', '2010', downloading(62), 'Dee · 4K · approved', 'asked'),
    item('Charade', '1963', requested, 'Sam · asked', 'asked'),
    item('Sprite Fright', '2021', requested, 'Marco · asked', 'asked'),
    item('Elephants Dream', '2006', requested, "From Priya's watchlist", 'asked'),
  ]),
};

export const coming: ComingEvent[] = [
  {
    day: 'Sat',
    date: '21',
    title: 'Sintel 4K',
    status: 'Downloading 62%',
    progress: 62,
    href: '/calendar',
  },
  {
    day: 'Sun',
    date: '22',
    title: 'Cosmos Laundromat',
    status: 'Searching sources',
    href: '/calendar',
  },
  {
    day: 'Tue',
    date: '24',
    title: 'Detour',
    status: 'Rechecking sources',
    href: '/calendar',
  },
  {
    day: 'Thu',
    date: '26',
    title: 'Pioneer One S1E4',
    status: 'Expected',
    href: '/calendar',
  },
];
