// Actions on a file that is already in the library: `internal/web/api_library_files.go`.
import { del, postJSON } from './api';
import type { LibraryFile } from './catalog';

/** What renaming did. `renamed` is false when the file already had the right name. */
export interface RenameResult {
  renamed: boolean;
  /** The old and the new path, under the library folder, with "/". */
  from: string;
  to: string;
  file: LibraryFile;
}

/** Give a file the name Marquee would give it today (the owner and admins). */
export function renameLibraryFile(id: number): Promise<RenameResult> {
  return postJSON<RenameResult>(`library/files/${id}/rename`, undefined, undefined, 30_000);
}

/** What deleting did. `wasMissing`: the file was already gone from the disk, so only the row went. */
export interface DeleteResult {
  deleted: boolean;
  path: string;
  fromDisk: boolean;
  wasMissing: boolean;
}

/**
 * Take a file out of the library and, with `fromDisk`, off the disk too (the owner). That cannot be
 * undone: ask first.
 */
export function deleteLibraryFile(id: number, fromDisk: boolean): Promise<DeleteResult> {
  return del<DeleteResult>(`library/files/${id}?deleteFile=${fromDisk ? 'true' : 'false'}`);
}
