// Mock data for Home, reproduced from the approved Cinema canvas (Main.dc.html).
// Strings here are content (what the server will send), not interface text.
import { slugify } from '../core/posters';
import type {
  CaptionTone,
  ComingEvent,
  FeaturedTitle,
  PosterItem,
  PosterRowData,
  Status,
  TitleKind,
} from '../core/types';

function item(
  title: string,
  year: string,
  status: Status,
  caption: string,
  tone: CaptionTone,
  kind: TitleKind = 'movie',
): PosterItem {
  return { id: slugify(title), kind, title, year, status, caption, tone };
}

const library: Status = { kind: 'library' };
const requested: Status = { kind: 'requested' };
const missing: Status = { kind: 'missing' };
const downloading = (pct: number): Status => ({ kind: 'downloading', pct });

export const featured: FeaturedTitle = {
  id: 'metropolis',
  kind: 'movie',
  title: 'Metropolis',
  year: 1927,
  quality: '1080p',
  server: 'Jellyfin',
  askQuality: '4K',
  meta: '1927 · Silent film · Directed by Fritz Lang',
  blurb:
    'A futuristic city is split between the planners who live above and the workers who run its machines below.',
  theme: {
    bg: '#16213A',
    artBg: '#0F1624',
    disc: '#2A3C63',
    towerA: '#1B2842',
    towerB: '#0A0F18',
  },
};

export const rows: PosterRowData[] = [
  {
    id: 'friends-want',
    title: 'Your friends want',
    href: '/requests',
    items: [
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
    ],
  },
  {
    id: 'recently-added',
    title: 'Recently added',
    href: '/movies',
    items: [
      item('Nosferatu', '1922', library, 'Added today · 1080p', 'quiet'),
      item('The General', '1926', library, 'Added today · 1080p', 'quiet'),
      item('Sherlock Jr.', '1924', library, 'Added yesterday · 1080p', 'quiet'),
      item('Safety Last!', '1923', library, 'Added Wed · 1080p', 'quiet'),
      item('The Kid', '1921', library, 'Added Wed · 1080p', 'quiet'),
      item('Night of the Living Dead', '1968', library, 'Added Mon · 1080p', 'quiet'),
      item('His Girl Friday', '1940', library, 'Added Mon · 1080p', 'quiet'),
    ],
  },
  {
    id: 'new-and-popular',
    title: 'New & popular',
    href: '/popular',
    items: [
      item('Big Buck Bunny', '2008', library, 'Short film · 1080p', 'quiet'),
      item('Tears of Steel', '2012', library, 'Short film · 1080p', 'quiet'),
      item('Cosmos Laundromat', '2015', requested, 'Short film · you asked', 'quiet'),
      item('Spring', '2019', library, 'Short film · 1080p', 'quiet'),
      item('Pioneer One', '2010 · TV', library, 'TV · 3 episodes', 'quiet', 'tv'),
      item('Undersea Kingdom', '1936 · TV', missing, 'TV · no release yet', 'quiet', 'tv'),
    ],
  },
];

export const coming: ComingEvent[] = [
  {
    day: 'Sat',
    date: '21',
    title: 'Sintel 4K',
    status: 'Downloading 62%',
    progress: 62,
    href: '/movies/sintel',
  },
  {
    day: 'Sun',
    date: '22',
    title: 'Cosmos Laundromat',
    status: 'Searching sources',
    href: '/movies/cosmos-laundromat',
  },
  {
    day: 'Tue',
    date: '24',
    title: 'Detour',
    status: 'Rechecking sources',
    href: '/movies/detour',
  },
  {
    day: 'Thu',
    date: '26',
    title: 'Pioneer One S1E4',
    status: 'Expected',
    href: '/tv/pioneer-one',
  },
];
