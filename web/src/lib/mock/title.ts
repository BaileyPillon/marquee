// Sample sections of the Movie page whose backends are not built yet (a 4K copy waiting for the
// owner, releases found, history), reproduced from the approved Cinema canvas (A-Title.dc.html).
// The page tags each of them "Preview". The sample takes the real title's name and year so it does
// not talk about some other film; everything else in it is made up.
import type { HistoryKind, Release } from '../core/views';
import type { Status } from '../core/types';

export interface TitlePreview {
  copy4k: { status: Status; headline: string; asked: string; rule: string };
  releases: { note: string; items: Release[] };
  history: Array<{ date: string; kind: HistoryKind; text: string }>;
}

/** "The Kid" -> "The.Kid", the way release names write a title. */
function dotted(name: string): string {
  return name.replace(/[^A-Za-z0-9]+/g, '.').replace(/^\.+|\.+$/g, '') || 'Title';
}

export function titlePreview(name: string, year: number): TitlePreview {
  const base = `${dotted(name)}${year ? `.${year}` : ''}`;
  return {
    copy4k: {
      status: { kind: 'requested' },
      headline: 'Priya asked for this in 4K · waiting for your yes',
      asked: 'Asked on 19 Nov',
      rule: 'Your 4K rule: 2160p, HDR preferred, under 60 GB · folder /movies-4k',
    },
    releases: {
      note: 'For the 4K copy · 3 found on 20 Nov',
      items: [
        {
          name: `${base}.2160p.UHD.BluRay.REMUX.HDR.HEVC-GRP`,
          size: '52.4 GB',
          source: 'Archive feed',
          age: '1 day',
          score: 94,
          reason: 'Best match: 2160p remux under your size limit',
          best: true,
        },
        {
          name: `${base}.2160p.UHD.BluRay.x265-GRP`,
          size: '24.9 GB',
          source: 'Archive feed',
          age: '1 day',
          score: 81,
          reason: 'Good fit: 2160p encode with HDR, less than half the size',
          best: false,
        },
        {
          name: `${base}.2160p.UHD.BluRay.REMUX.DV.HEVC-GRP`,
          size: '68.1 GB',
          source: 'Usenet feed',
          age: '5 days',
          score: 52,
          reason: 'Too big: 68.1 GB is over your 60 GB limit',
          best: false,
        },
      ],
    },
    history: [
      { date: '20 Nov', kind: 'found', text: '3 4K releases found' },
      { date: '19 Nov', kind: 'asked', text: 'Priya asked for 4K' },
      { date: '3 Nov', kind: 'imported', text: 'Imported Bluray-1080p · Jellyfin rescanned' },
      { date: '3 Nov', kind: 'grabbed', text: "Grabbed from Torznab source 'Archive feed'" },
    ],
  };
}
