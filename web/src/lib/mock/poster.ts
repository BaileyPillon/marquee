// Small builders shared by the mock modules: poster cards and their statuses.
import { slugify } from '../core/posters';
import type { CaptionTone, PosterItem, Status, TitleKind } from '../core/types';

export const library: Status = { kind: 'library' };
export const requested: Status = { kind: 'requested' };
export const missing: Status = { kind: 'missing' };
export const downloading = (pct: number): Status => ({ kind: 'downloading', pct });

export function item(
  title: string,
  year: string,
  status: Status,
  caption: string,
  tone: CaptionTone,
  kind: TitleKind = 'movie',
): PosterItem {
  return { id: slugify(title), kind, title, year, status, caption, tone };
}
