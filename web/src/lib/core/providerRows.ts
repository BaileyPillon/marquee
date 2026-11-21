// The Title info rows (TMDB, TVmaze, MusicBrainz) as Settings shows them, built from what
// `GET /metadata/status` really says: whether the owner's TMDB key is saved and how each provider
// last fared.
import type { MetadataStatus, ProviderStatus } from './catalog';
import { formatAgo } from './format';
import { t } from './i18n';
import type { ConnectionRow } from './views';

/** "last worked 5 minutes ago", or the last failure when it is newer than the last success. */
function lastTry(p: ProviderStatus | undefined): { ok: boolean; text: string } {
  if (!p) return { ok: true, text: t('info.notUsedYet') };
  const okAt = p.lastOk ? Date.parse(p.lastOk) : 0;
  const errAt = p.lastErrorAt ? Date.parse(p.lastErrorAt) : 0;
  if (errAt > okAt) {
    return { ok: false, text: t('info.lastFailed', { ago: formatAgo(p.lastErrorAt) }) };
  }
  if (okAt) return { ok: true, text: t('info.lastWorked', { ago: formatAgo(p.lastOk) }) };
  return { ok: true, text: t('info.notUsedYet') };
}

export function providerRows(status: MetadataStatus): ConnectionRow[] {
  const tmdb = lastTry(status.providers.tmdb);
  const tvmaze = lastTry(status.providers.tvmaze);
  const musicbrainz = lastTry(status.providers.musicbrainz);
  return [
    {
      name: 'TMDB',
      tag: t('info.movies'),
      status: status.hasKey
        ? t('info.tmdbKey', { last: tmdb.text })
        : t('info.tmdbNoKey'),
      state: status.hasKey && tmdb.ok ? 'on' : 'off',
      action: 'edit',
    },
    {
      name: 'TVmaze',
      tag: t('info.shows'),
      status: t('info.noKeyNeeded', { last: tvmaze.text }),
      state: tvmaze.ok ? 'on' : 'off',
    },
    {
      name: 'MusicBrainz',
      tag: t('info.music'),
      status: t('info.noKeyNeeded', { last: musicbrainz.text }),
      state: musicbrainz.ok ? 'on' : 'off',
    },
  ];
}
