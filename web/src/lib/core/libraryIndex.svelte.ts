// Which titles, episodes and tracks have a file in the library.
//
// The catalog says what Marquee knows about; only the library's file list says what is on disk, and
// `GET /library/files` is for owners and admins. So this index fills for them and stays
// "unavailable" for members, and the pages that use it say "Unknown" or leave a badge off rather
// than guess. (A backend field on the title and episode would remove this join.)
import { ApiError } from './api';
import { getLibraryFiles, type LibraryFile } from './catalog';
import { session } from './session.svelte';

export interface TitleFiles {
  count: number;
  bytes: number;
  /** The quality of the biggest file, as the scanner read it from the name. */
  quality: string;
  files: LibraryFile[];
}

export type IndexStatus = 'idle' | 'loading' | 'ready' | 'unavailable';

const PAGE = 500;
const MAX_PAGES = 20;

class LibraryIndex {
  status = $state<IndexStatus>('idle');
  titles = $state.raw<ReadonlyMap<number, TitleFiles>>(new Map());
  episodes = $state.raw<ReadonlyMap<number, LibraryFile>>(new Map());
  tracks = $state.raw<ReadonlyMap<number, LibraryFile>>(new Map());

  #user: number | null = null;
  #run = 0;

  /** Start loading if nobody has yet. Safe to call from every page that needs the index. */
  ensure(): void {
    const me = session.me;
    if (!me) {
      this.#reset();
      return;
    }
    if (this.#user !== me.id) this.#reset();
    if (this.status !== 'idle') return;
    this.#user = me.id;
    if (me.role === 'member') {
      this.status = 'unavailable';
      return;
    }
    void this.#load();
  }

  /** Ask again (a scan just finished). */
  reload(): void {
    this.#run += 1;
    this.status = 'idle';
    this.ensure();
  }

  #reset(): void {
    this.#run += 1;
    this.#user = null;
    this.status = 'idle';
    this.titles = new Map();
    this.episodes = new Map();
    this.tracks = new Map();
  }

  async #load(): Promise<void> {
    const run = ++this.#run;
    this.status = 'loading';
    const titles = new Map<number, TitleFiles>();
    const episodes = new Map<number, LibraryFile>();
    const tracks = new Map<number, LibraryFile>();
    try {
      let offset = 0;
      for (let page = 0; page < MAX_PAGES; page++) {
        const res = await getLibraryFiles({ state: 'matched', limit: PAGE, offset });
        if (run !== this.#run) return;
        for (const file of res.files) {
          if (file.titleId !== null) {
            const entry = titles.get(file.titleId) ?? { count: 0, bytes: 0, quality: '', files: [] };
            const biggest = entry.files.every((f) => f.size <= file.size);
            entry.count += 1;
            entry.bytes += file.size;
            if (biggest && file.quality) entry.quality = file.quality;
            entry.files.push(file);
            titles.set(file.titleId, entry);
          }
          for (const id of file.episodeIds ?? []) episodes.set(id, file);
          if (file.trackId !== null) tracks.set(file.trackId, file);
        }
        offset += res.files.length;
        if (res.files.length === 0 || offset >= res.total) break;
      }
    } catch (err) {
      if (run !== this.#run) return;
      // Not allowed or not there: say so for good. Anything else (a slow answer) may be tried again.
      const final = err instanceof ApiError && (err.status === 403 || err.status === 404);
      this.status = final ? 'unavailable' : 'idle';
      return;
    }
    this.titles = titles;
    this.episodes = episodes;
    this.tracks = tracks;
    this.status = 'ready';
  }
}

export const libraryIndex = new LibraryIndex();
