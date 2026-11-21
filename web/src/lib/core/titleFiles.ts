// What the library holds for a title, episode or track. The server now says it on every title and
// episode (`fileCount`, `quality`, `inLibrary`, `sizeBytes`; `hasFile`, `quality`, `size`), and every
// signed-in person may read it. An older server leaves them out; then the owner's and admins' file
// list (`libraryIndex`) is joined instead, and members see nothing rather than a guess.
import type { Episode, Title, Track } from './catalog';
import { libraryIndex } from './libraryIndex.svelte';

export interface FileInfo {
  inLibrary: boolean;
  /** The best quality, as the scanner read it ("Bluray-1080p"); empty when none. */
  quality: string;
  sizeBytes: number;
  count: number;
}

/** What is on disk for a title, or undefined when it is not known (older server, member). */
export function titleFileInfo(
  title: Pick<Title, 'id' | 'inLibrary' | 'quality' | 'sizeBytes' | 'fileCount'>,
): FileInfo | undefined {
  if (title.inLibrary !== undefined) {
    return {
      inLibrary: title.inLibrary,
      quality: title.quality ?? '',
      sizeBytes: title.sizeBytes ?? 0,
      count: title.fileCount ?? 0,
    };
  }
  if (libraryIndex.status !== 'ready') return undefined;
  const files = libraryIndex.titles.get(title.id);
  return files
    ? { inLibrary: true, quality: files.quality, sizeBytes: files.bytes, count: files.count }
    : { inLibrary: false, quality: '', sizeBytes: 0, count: 0 };
}

export interface EpisodeFile {
  has: boolean;
  quality: string;
  size: number;
}

/** Does the episode have a file? Undefined when that is not known. */
export function episodeFile(ep: Pick<Episode, 'id' | 'hasFile' | 'quality' | 'size'>): EpisodeFile | undefined {
  if (ep.hasFile !== undefined) return { has: ep.hasFile, quality: ep.quality ?? '', size: ep.size ?? 0 };
  if (libraryIndex.status !== 'ready') return undefined;
  const file = libraryIndex.episodes.get(ep.id);
  return file
    ? { has: true, quality: file.quality, size: file.size }
    : { has: false, quality: '', size: 0 };
}

export interface TrackFile {
  has: boolean;
  quality: string;
  /** The file's extension, upper case, when the library's own list is the source. */
  format: string;
}

export function trackFile(
  track: Pick<Track, 'id'> & { hasFile?: boolean; quality?: string },
): TrackFile | undefined {
  if (track.hasFile !== undefined) {
    return { has: track.hasFile, quality: track.quality ?? '', format: '' };
  }
  if (libraryIndex.status !== 'ready') return undefined;
  const file = libraryIndex.tracks.get(track.id);
  return file
    ? { has: true, quality: file.quality, format: file.relPath.split('.').pop()?.toUpperCase() ?? '' }
    : { has: false, quality: '', format: '' };
}
