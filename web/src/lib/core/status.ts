import { t } from './i18n';
import type { Status } from './types';

export type BadgeTone = 'solid' | 'dashed' | 'inverse';

/** Library and downloading: solid outline. Requested: dashed outline. Missing: filled. */
export function statusTone(status: Status): BadgeTone {
  switch (status.kind) {
    case 'requested':
      return 'dashed';
    case 'missing':
      return 'inverse';
    default:
      return 'solid';
  }
}

export function statusLabel(status: Status): string {
  switch (status.kind) {
    case 'downloading':
      return t('status.downloading', { pct: status.pct });
    case 'library':
      return t('status.library');
    case 'requested':
      return t('status.requested');
    case 'missing':
      return t('status.missing');
  }
}
