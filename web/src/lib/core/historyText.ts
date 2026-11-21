// A title's history in plain words. The server records an event word (`grabbed`, `imported`, ...)
// and a `detail` that is a sentence, or an object with the particulars (`{by, quality, reason, ...}`).
// Whatever arrives, a line is written for it; unknown particulars are simply left out.
import { formatBytes } from './format';
import { t } from './i18n';
import type { HistoryItem } from './serversApi';
import type { IconName } from '../ui/icons';

export const historyIcon: Record<string, IconName> = {
  grabbed: 'downloads',
  imported: 'check',
  upgraded: 'upgrade',
  failed: 'warning',
  deleted: 'trash',
  renamed: 'edit',
  requested: 'requests',
  approved: 'check',
  declined: 'close',
};

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function quality(value: unknown): string {
  const q = text(value);
  if (q === 'hd') return t('ask.quality.hd.short');
  if (q === 'uhd') return t('ask.quality.uhd.short');
  return q;
}

/** The line shown for one history entry. */
export function historyText(item: HistoryItem): string {
  const d = item.detail;
  if (typeof d === 'string' && d.trim()) return d.trim();
  const o: Record<string, unknown> = d && typeof d === 'object' ? (d as Record<string, unknown>) : {};
  const own = text(o['text']) || text(o['message']) || text(o['summary']);
  if (own) return own;

  const q = quality(o['quality']);
  const by = text(o['by']);
  const release = text(o['release']) || text(o['releaseTitle']) || text(o['title']);
  const source = text(o['source']) || text(o['indexer']) || text(o['client']);
  const reason = text(o['reason']) || text(o['error']);
  const size = typeof o['size'] === 'number' && o['size'] > 0 ? formatBytes(o['size']) : '';
  const from = quality(o['from']);
  const to = quality(o['to']);

  switch (item.event) {
    case 'requested': {
      const user = text(o['user']);
      if (user && q) return t('history.requested.userQuality', { user, quality: q });
      if (user) return t('history.requested.user', { user });
      return q ? t('history.requested.quality', { quality: q }) : t('history.event.requested');
    }
    case 'approved':
      if (o['auto'] === true) return q ? t('history.approved.autoQuality', { quality: q }) : t('history.approved.auto');
      if (by) return q ? t('history.approved.byQuality', { by, quality: q }) : t('history.approved.by', { by });
      return q ? t('history.approved.quality', { quality: q }) : t('history.event.approved');
    case 'declined': {
      const base = by
        ? q
          ? t('history.declined.byQuality', { by, quality: q })
          : t('history.declined.by', { by })
        : t('history.event.declined');
      return reason ? `${base} — ‘${reason}’` : base;
    }
    case 'grabbed':
      if (release && source) return t('history.grabbed.releaseSource', { release, source });
      if (release) return t('history.grabbed.release', { release });
      return source ? t('history.grabbed.source', { source }) : t('history.event.grabbed');
    case 'imported':
      return [q ? t('history.imported.quality', { quality: q }) : t('history.event.imported'), size]
        .filter(Boolean)
        .join(' · ');
    case 'upgraded':
      return from && to ? t('history.upgraded.fromTo', { from, to }) : t('history.event.upgraded');
    case 'failed':
      return reason ? t('history.failed.why', { why: reason }) : t('history.event.failed');
    case 'deleted':
      return t('history.event.deleted');
    case 'renamed':
      return t('history.event.renamed');
    default:
      return item.event;
  }
}
