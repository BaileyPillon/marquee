// Mock data for Requests, reproduced from the approved Cinema canvas (A-Requests.dc.html for the
// desktop screen, A-Approve.dc.html for the phone). getRequests() and decideRequest() have the
// shape of the future API calls.
//
// The phone card of the second ask (The Phantom Creeps) is not drawn on the canvas: its
// `phoneFacts` follow the pattern of the first one.
import type { RequestDecision, RequestsView } from '../core/views';
import { respond } from './latency';

const requests: RequestsView = {
  decidedThisWeek: 5,
  waiting: [
    {
      id: 'ask-metropolis-4k',
      requester: 'Priya',
      who: 'Priya · today 08:30',
      ago: '12 minutes ago',
      title: 'Metropolis',
      year: 1927,
      posterYear: '1927',
      posterMeta: '1927 · Film',
      quality: '4K',
      what: '4K (2160p) · separate copy',
      brief: 'Priya · 4K',
      facts: [
        { label: 'Size', value: 'About 38 GB' },
        { label: 'Folder', value: '/movies-4k' },
        { label: 'Her 4K asks this week', value: '1 of 2' },
        { label: 'Already in library', value: '1080p' },
        { label: "Why it's waiting", value: '4K asks always need your yes' },
      ],
      phoneFacts: [
        { label: 'Quality', value: '4K (2160p) — a separate copy' },
        { label: 'Size', value: 'About 38 GB' },
        { label: 'Folder', value: '/movies-4k' },
        { label: "Priya's 4K asks this week", value: '1 of 2' },
      ],
    },
    {
      id: 'ask-phantom-creeps',
      requester: 'Dee',
      who: 'Dee · yesterday 21:10',
      ago: 'yesterday',
      title: 'The Phantom Creeps',
      year: 1939,
      posterYear: '1939 · TV',
      posterMeta: '1939 · TV',
      quality: 'HD',
      what: 'All 12 episodes · HD (1080p)',
      brief: 'Dee · all episodes',
      facts: [
        { label: 'Size', value: 'About 9 GB' },
        { label: 'Folder', value: '/tv' },
        { label: "Dee's HD asks this week", value: '2 of 10' },
        { label: 'Already in library', value: 'Nothing yet' },
        { label: "Why it's waiting", value: 'Auto-approve is off for Dee' },
      ],
      phoneFacts: [
        { label: 'Quality', value: 'HD (1080p)' },
        { label: 'Size', value: 'About 9 GB' },
        { label: 'Folder', value: '/tv' },
        { label: "Dee's HD asks this week", value: '2 of 10' },
      ],
    },
  ],
  decided: [
    {
      id: 'sherlock-jr',
      title: 'Sherlock Jr.',
      detail: 'Claude for Sam (Sam confirmed) · approved automatically',
      outcome: 'approved',
      when: '19 Nov',
      fromWatchlist: false,
    },
    {
      id: 'cosmos-laundromat',
      title: 'Cosmos Laundromat',
      detail: 'You asked · no approval needed',
      outcome: 'approved',
      when: '19 Nov',
      fromWatchlist: false,
    },
    {
      id: 'nosferatu',
      title: 'Nosferatu',
      detail: 'Sam · approved automatically (HD)',
      outcome: 'approved',
      when: '18 Nov',
      fromWatchlist: false,
    },
    {
      id: 'charade',
      title: 'Charade',
      detail: "Marco's Plex watchlist · approved automatically",
      outcome: 'approved',
      when: '18 Nov',
      fromWatchlist: true,
    },
    {
      id: 'plan-9',
      title: 'Plan 9 from Outer Space',
      detail: "Dee · declined by you — 'not this one'",
      outcome: 'declined',
      when: '17 Nov',
      fromWatchlist: false,
    },
  ],
  people: [
    { name: 'Priya', hd: '3 of 10', fourK: '1 of 2', autoApprove: true },
    { name: 'Sam', hd: '2 of 10', fourK: '0 of 2', autoApprove: true },
    { name: 'Marco', hd: '1 of 10', fourK: '0 of 2', autoApprove: true },
    { name: 'Dee', hd: '2 of 10', fourK: '1 of 2', autoApprove: false },
  ],
  watchlists: {
    headline: 'Plex friends: 3 linked · checked 10 min ago',
    detail: 'Priya, Sam and Marco are linked. Dee has not linked Plex yet.',
  },
};

/** The owner's request queue. The API will answer GET /api/v1/requests. */
export function getRequests(): Promise<RequestsView> {
  return respond(requests);
}

/** Approve or decline one ask. The API will answer POST /api/v1/requests/:id/decision. */
export function decideRequest(_id: string, _decision: RequestDecision): Promise<void> {
  return respond(undefined);
}
