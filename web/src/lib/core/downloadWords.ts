// A download in plain words: the state line and the stats line. Shared by the Downloads page and
// the live strip on a title page.
import type { DownloadState, DownloadView } from './downloadsApi';
import { formatEta, formatSpeed } from './format';
import { t } from './i18n';

/** "Downloading", "Queued — waiting for a free slot", "Finished — waiting to be imported", ... */
export function stateText(state: DownloadState): string {
  switch (state) {
    case 'downloading':
      return t('downloads.state.downloading');
    case 'stalled':
      return t('downloads.state.stalled');
    case 'paused':
      return t('downloads.state.paused');
    case 'queued':
      return t('downloads.state.queued');
    case 'completed':
      return t('downloads.state.completed');
    case 'importing':
      return t('downloads.state.importing');
    default:
      return state;
  }
}

/** "62% · 14.2 MB/s · 6 min left · qBittorrent". */
export function statsOf(d: DownloadView): string {
  const live = d.state === 'downloading' || d.state === 'stalled';
  return [
    `${Math.round(d.progress * 100)}%`,
    live && d.speedBps > 0 ? formatSpeed(d.speedBps) : '',
    live ? formatEta(d.etaSeconds) : '',
    d.client.name,
  ]
    .filter(Boolean)
    .join(' · ');
}

/** Which dot a state gets: solid (moving), accent (importing), hollow (waiting or stopped). */
export function dotOf(state: DownloadState): 'solid' | 'accent' | 'hollow' {
  if (state === 'downloading') return 'solid';
  if (state === 'completed' || state === 'importing') return 'accent';
  return 'hollow';
}
