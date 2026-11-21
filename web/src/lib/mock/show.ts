// Mock data for the Show page, reproduced from the approved Cinema canvas (A-Show.dc.html).
// getShow() has the shape of the future API call; any id answers with the same show.
import type { ShowView } from '../core/views';
import { respond } from './latency';
import { item, missing, requested } from './poster';

const pioneerOne: ShowView = {
  id: 'pioneer-one',
  title: 'Pioneer One',
  year: 2010,
  status: 'In your library · 3 of 6 episodes',
  meta: '2010 · Drama · 1 season · Creative Commons',
  blurb:
    'A Mars-returning cosmonaut lands in rural Canada and the U.S. and Canadian governments argue over what to do.',
  theme: { bg: '#241B2A', artBg: '#2A2030' },
  server: 'Jellyfin',
  watching: true,
  season: {
    number: 1,
    have: 3,
    total: 6,
    episodes: [
      {
        num: 1,
        air: 'Thu 22 Oct',
        state: 'library',
        status: 'In library · 1080p · 1.1 GB',
        action: 'open',
      },
      {
        num: 2,
        air: 'Thu 29 Oct',
        state: 'library',
        status: 'In library · 1080p · 1.1 GB',
        action: 'open',
      },
      {
        num: 3,
        air: 'Thu 5 Nov',
        state: 'library',
        status: 'In library · 720p · 0.7 GB',
        action: 'open',
      },
      {
        num: 4,
        air: 'Thu 12 Nov',
        state: 'downloading',
        status: 'Downloading 41% · SABnzbd',
        progress: 41,
        action: 'search',
      },
      {
        num: 5,
        air: 'Thu 19 Nov',
        state: 'missing',
        status: 'Missing · searching',
        action: 'search',
      },
      {
        num: 6,
        air: 'Thu 26 Nov',
        state: 'upcoming',
        status: 'Not aired yet · expected Thu 26 Nov',
        action: 'search',
      },
    ],
  },
  rules: [
    { label: 'Quality', value: 'HD 1080p, fall back to 720p' },
    { label: 'Folder', value: '/tv/Pioneer One' },
    { label: 'Subtitles', value: 'English' },
    { label: 'Asked for by', value: 'Dee' },
  ],
  similar: [
    item('Undersea Kingdom', '1936 · TV', missing, 'TV · no release yet', 'quiet', 'tv'),
    item(
      'The Phantom Creeps',
      '1939 · TV',
      requested,
      'Dee · all episodes · waiting for you',
      'waiting',
      'tv',
    ),
  ],
};

/** The show page for `id`. The API will answer GET /api/v1/shows/:id. */
export function getShow(_id: string): Promise<ShowView> {
  return respond(pioneerOne);
}
