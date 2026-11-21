// Mock data for the Movie page, reproduced from the approved Cinema canvas (A-Title.dc.html).
// getTitle() has the shape of the future API call; any id answers with the same title.
import type { TitleView } from '../core/views';
import { respond } from './latency';
import { item, library, missing, requested } from './poster';

const metropolis: TitleView = {
  id: 'metropolis',
  title: 'Metropolis',
  year: 1927,
  status: 'In your library · 1080p',
  meta: '1927 · 2 h 33 min · Silent, German intertitles · Directed by Fritz Lang',
  blurb:
    'A futuristic city is split between the planners who live above and the workers who run its machines below.',
  theme: {
    bg: '#16213A',
    artBg: '#0F1624',
    disc: '#2A3C63',
    towerA: '#1B2842',
    towerB: '#0A0F18',
  },
  genres: ['Science fiction', 'Drama'],
  server: 'Jellyfin',
  askQuality: '4K',
  library: {
    quality: 'Bluray-1080p',
    fileName: 'Metropolis (1927) Bluray-1080p.mkv',
    details: '14.2 GB · /movies/Metropolis (1927)/ · added 3 Nov · English subtitles',
  },
  copy4k: {
    status: requested,
    headline: 'Priya asked for this in 4K · waiting for your yes',
    asked: 'Asked on 19 Nov',
    rule: 'Your 4K rule: 2160p, HDR preferred, under 60 GB · folder /movies-4k',
  },
  releases: {
    note: 'For the 4K copy · 3 found on 20 Nov',
    items: [
      {
        name: 'Metropolis.1927.2160p.UHD.BluRay.REMUX.HDR.HEVC-GRP',
        size: '52.4 GB',
        source: 'Archive feed',
        age: '1 day',
        score: 94,
        reason: 'Best match: 2160p remux under your size limit',
        best: true,
      },
      {
        name: 'Metropolis.1927.2160p.UHD.BluRay.x265-GRP',
        size: '24.9 GB',
        source: 'Archive feed',
        age: '1 day',
        score: 81,
        reason: 'Good fit: 2160p encode with HDR, less than half the size',
        best: false,
      },
      {
        name: 'Metropolis.1927.2160p.UHD.BluRay.REMUX.DV.HEVC-GRP',
        size: '68.1 GB',
        source: 'Usenet feed',
        age: '5 days',
        score: 52,
        reason: 'Too big: 68.1 GB is over your 60 GB limit',
        best: false,
      },
    ],
  },
  cast: ['Brigitte Helm', 'Alfred Abel', 'Gustav Fröhlich', 'Rudolf Klein-Rogge'],
  crew: [
    { name: 'Fritz Lang', role: 'director' },
    { name: 'Thea von Harbou', role: 'writer' },
  ],
  history: [
    { date: '20 Nov', kind: 'found', text: '3 4K releases found' },
    { date: '19 Nov', kind: 'asked', text: 'Priya asked for 4K' },
    { date: '3 Nov', kind: 'imported', text: 'Imported Bluray-1080p · Jellyfin rescanned' },
    { date: '3 Nov', kind: 'grabbed', text: "Grabbed from Torznab source 'Archive feed'" },
  ],
  similar: [
    item('Nosferatu', '1922', library, 'Silent film · 1080p', 'quiet'),
    item('The General', '1926', library, 'Silent film · 1080p', 'quiet'),
    item('Sherlock Jr.', '1924', library, 'Silent film · 1080p', 'quiet'),
    item('The Kid', '1921', library, 'Silent film · 1080p', 'quiet'),
    item('Safety Last!', '1923', library, 'Silent film · 1080p', 'quiet'),
    item('Detour', '1945', missing, "From Marco's watchlist", 'asked'),
  ],
};

/** The movie page for `id`. The API will answer GET /api/v1/movies/:id. */
export function getTitle(_id: string): Promise<TitleView> {
  return respond(metropolis);
}
