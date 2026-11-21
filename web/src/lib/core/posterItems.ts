// Catalog titles as poster cards: the status badge, the caption and the artwork.
import type { ApiKind, Title } from './catalog';
import { formatAdded, posterYear } from './format';
import { titleFileInfo } from './titleFiles';
import type { PosterItem, Status, TitleKind } from './types';

/** The UI says "tv" in its own paths and types where the server says "show". */
export function uiKind(kind: ApiKind): TitleKind {
  return kind === 'show' ? 'tv' : kind;
}

/**
 * In library when a file is known, Missing when it is wanted and has none. No badge when the
 * library's file list is not known (a member cannot read it) or the title is not monitored.
 */
export function statusOfTitle(
  title: Pick<Title, 'id' | 'monitored' | 'inLibrary' | 'quality' | 'sizeBytes' | 'fileCount'>,
): Status | undefined {
  const info = titleFileInfo(title);
  if (!info) return undefined;
  if (info.inLibrary) return { kind: 'library' };
  return title.monitored ? { kind: 'missing' } : undefined;
}

export function posterItem(title: Title): PosterItem {
  const status = statusOfTitle(title);
  return {
    id: String(title.id),
    kind: uiKind(title.kind),
    title: title.name,
    year: posterYear(title),
    ...(status ? { status } : {}),
    caption: formatAdded(title.addedAt),
    tone: 'quiet',
    ...(title.posterRef ? { imageRef: title.posterRef } : {}),
  };
}
