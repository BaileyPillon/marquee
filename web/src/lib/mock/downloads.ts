// Mock data for Downloads, reproduced from the approved Cinema canvas (A-Downloads.dc.html).
// getDownloads() has the shape of the future API call.
import type { DownloadsView } from '../core/views';
import { respond } from './latency';

const downloads: DownloadsView = {
  counts: { downloading: 2, importing: 1, queued: 1 },
  connectedApps: 'qBittorrent and SABnzbd',
  now: [
    {
      id: 'sintel',
      title: 'Sintel (2010)',
      quality: '2160p',
      pct: 62,
      kind: 'downloading',
      step: 'Downloading',
      stats: '62% · 14.2 MB/s · 6 min left · qBittorrent',
      canPause: true,
    },
    {
      id: 'pioneer-one-s1e4',
      title: 'Pioneer One S1E4',
      quality: '1080p',
      pct: 41,
      kind: 'downloading',
      step: 'Downloading',
      stats: '41% · 3.1 MB/s · 9 min left · SABnzbd',
      canPause: true,
    },
    {
      id: 'tears-of-steel',
      title: 'Tears of Steel (2012)',
      quality: '1080p',
      pct: 100,
      kind: 'importing',
      step: 'Importing: checking file → renaming → moving to /movies',
      stats: '100% · qBittorrent',
      canPause: false,
    },
    {
      id: 'cosmos-laundromat',
      title: 'Cosmos Laundromat (2015)',
      quality: '1080p',
      pct: 0,
      kind: 'queued',
      step: 'Queued — waiting for a free slot',
      stats: 'Next in line',
      canPause: true,
    },
  ],
  importing: {
    title: 'Tears of Steel (2012)',
    quality: '1080p',
    server: 'Jellyfin',
    steps: [
      { label: 'Downloaded', state: 'done', detail: '2.2 GB from qBittorrent', mono: false },
      { label: 'Checked', state: 'done', detail: 'A real video, not a sample', mono: false },
      {
        label: 'Renamed',
        state: 'done',
        detail: 'Tears of Steel (2012)/Tears of Steel (2012) Bluray-1080p.mkv',
        mono: true,
      },
      { label: 'Moved', state: 'working', detail: 'Moving to /movies', mono: false },
      {
        label: 'Jellyfin rescanned',
        state: 'pending',
        detail: 'Starts after the move',
        mono: false,
      },
    ],
  },
  apps: [
    { name: 'qBittorrent', status: 'Connected · 2 active · 410 GB free on M:', connected: true },
    { name: 'SABnzbd', status: 'Connected · 1 active', connected: true },
    { name: 'Deluge', status: 'Not set up', connected: false },
  ],
  history: [
    {
      id: 'h-nosferatu',
      when: 'Today, 10:48',
      title: 'Nosferatu (1922) · 1080p',
      outcome: 'imported',
      what: 'Imported · Jellyfin rescanned',
      source: 'qBittorrent',
      size: '2.3 GB',
    },
    {
      id: 'h-the-general',
      when: 'Today, 09:15',
      title: 'The General (1926) · 1080p',
      outcome: 'imported',
      what: 'Imported · Jellyfin rescanned',
      source: 'SABnzbd',
      size: '1.9 GB',
    },
    {
      id: 'h-sprite-fright',
      when: 'Today, 07:40',
      title: 'Sprite Fright (2021) · 1080p',
      outcome: 'failed',
      what: 'Failed: file was a sample — searching again',
      source: 'SABnzbd',
      size: '48 MB',
    },
    {
      id: 'h-sherlock-jr',
      when: 'Yesterday, 21:05',
      title: 'Sherlock Jr. (1924) · 1080p',
      outcome: 'upgraded',
      what: 'Upgraded 720p → 1080p',
      source: 'qBittorrent',
      size: '2.1 GB',
    },
    {
      id: 'h-undersea-kingdom',
      when: 'Yesterday, 14:22',
      title: 'Undersea Kingdom (1936) · 480p',
      outcome: 'removed',
      what: 'Removed by you',
      source: 'qBittorrent',
      size: '310 MB',
    },
    {
      id: 'h-safety-last',
      when: 'Wed, 19:30',
      title: 'Safety Last! (1923) · 1080p',
      outcome: 'imported',
      what: 'Imported · Jellyfin rescanned',
      source: 'SABnzbd',
      size: '1.7 GB',
    },
  ],
};

/** Everything on the Downloads screen. The API will answer GET /api/v1/downloads. */
export function getDownloads(): Promise<DownloadsView> {
  return respond(downloads);
}
