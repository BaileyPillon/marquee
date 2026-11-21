// Mock data for the Ask sheet, reproduced from the approved Cinema canvas (A-Ask.dc.html).
// getAskOptions() has the shape of the future API call; any id answers with the same options.
import type { AskView } from '../core/views';
import { respond } from './latency';

const ask: AskView = {
  ownerName: 'Bailey',
  choices: [
    {
      id: 'hd',
      title: 'HD (1080p) — already in the library',
      sub: 'You can watch this now',
      unavailable: true,
    },
    {
      id: '4k',
      title: '4K (2160p)',
      sub: 'A separate copy, kept next to the HD one',
      unavailable: false,
    },
  ],
  selected: '4k',
  facts: [
    { label: 'Size', value: 'About 38 GB' },
    { label: 'Approval', value: "Needs Bailey's yes — 4K asks always do" },
    { label: 'Your 4K asks this week', value: '0 of 2 used' },
  ],
};

/** Quality choices and limits for asking for a title. The API will answer GET /api/v1/asks/options. */
export function getAskOptions(_titleId: string): Promise<AskView> {
  return respond(ask);
}
