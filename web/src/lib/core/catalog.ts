// The catalog, library, settings and requests APIs: their shapes and the calls that fetch them.
// Field names follow the Go handlers exactly (internal/web/api_titles.go, api_library.go,
// api_settings.go). Pages import from here and never build an /api/v1 address themselves.
import { ApiError, getJSON, patchJSON, postJSON, putJSON, del } from './api';
import type { MessageKey } from './i18n';

/** The server's word for a kind. The UI says "tv" in its own paths and types; see `titlePath`. */
export type ApiKind = 'movie' | 'show' | 'artist';

/* ------------------------------------------------------------------ Titles */

export interface Title {
  id: number;
  kind: ApiKind;
  tmdbId?: number;
  tvmazeId?: number;
  imdbId?: string;
  mbid?: string;
  name: string;
  sortName: string;
  altNames: string[];
  year: number;
  overview: string;
  tagline: string;
  certification: string;
  /** A YouTube video key. */
  trailerKey: string;
  runtimeMin: number;
  genres: string[];
  /** Loaded from `/api/v1/artwork/<ref>`. Empty when there is none. */
  posterRef: string;
  backdropRef: string;
  status: string;
  monitored: boolean;
  rootFolder: string;
  addedAt: string;
  updatedAt: string;
  refreshedAt: string;
}

export interface Season {
  number: number;
  monitored: boolean;
  episodeCount: number;
}

export interface Episode {
  id: number;
  season: number;
  number: number;
  name: string;
  /** YYYY-MM-DD, empty when unknown. */
  airDate: string;
  overview: string;
  runtimeMin: number;
  monitored: boolean;
}

export interface Track {
  id: number;
  disc: number;
  position: number;
  name: string;
  lengthMs: number;
  mbid: string;
}

export interface Album {
  id: number;
  mbid: string;
  name: string;
  year: number;
  /** album or ep */
  type: string;
  monitored: boolean;
  coverRef: string;
  tracks: Track[];
}

export interface Person {
  name: string;
  /** actor, director, writer or creator */
  role: string;
  /** The character, or the crew job. */
  job: string;
  imageRef: string;
}

export interface TitleDetail extends Title {
  seasons: Season[];
  episodes: Episode[];
  albums: Album[];
  people: Person[];
}

export interface TitleList {
  items: Title[];
  total: number;
  limit: number;
  offset: number;
}

export type SortOrder = 'added' | 'name' | 'year';

export interface SearchHit {
  kind: ApiKind;
  provider: string;
  providerId: string;
  name: string;
  year: number;
  overview: string;
  poster: { ref: string; kind?: string };
  imdbId?: string;
  /** A short hint that tells namesakes apart: an artist's type and country, a show's network. */
  detail?: string;
  inLibrary: boolean;
  titleId?: number;
}

/** One kind's share of a search: its results, or why it has none (`tmdb_key_missing`, say). */
export type SearchGroup =
  | { results: SearchHit[] }
  | { error: string; message: string; results?: undefined };

/** Providers can take a while (MusicBrainz especially); the server itself gives up after 25 s. */
const SEARCH_TIMEOUT_MS = 35_000;
/** Adding a title fetches everything the provider knows about it, in one go. */
const ADD_TIMEOUT_MS = 70_000;

/** `/api/v1/artwork/<ref>` for a poster, backdrop, cover or image ref. */
export function artworkUrl(ref: string): string {
  return `/api/v1/artwork/${ref.split('/').map(encodeURIComponent).join('/')}`;
}

/** The page of a title in the UI's own paths. */
export function titlePath(kind: ApiKind, id: number | string): string {
  if (kind === 'show') return `/tv/${id}`;
  if (kind === 'artist') return `/music/artists/${id}`;
  return `/movies/${id}`;
}

/** The library grid a kind belongs to. */
export function listPath(kind: ApiKind): string {
  if (kind === 'show') return '/tv';
  if (kind === 'artist') return '/music';
  return '/movies';
}

function query(params: Record<string, string | number | undefined>): string {
  const parts: string[] = [];
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue;
    parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`);
  }
  return parts.length ? `?${parts.join('&')}` : '';
}

/** Search one kind at its provider. A failure of the whole call comes back as an error group. */
export async function searchTitles(
  q: string,
  kind: ApiKind,
  signal?: AbortSignal,
): Promise<SearchGroup> {
  try {
    const res = await getJSON<Partial<Record<ApiKind, SearchGroup>>>(
      `search${query({ q, kind })}`,
      signal,
      SEARCH_TIMEOUT_MS,
    );
    return res[kind] ?? { results: [] };
  } catch (err) {
    if (signal?.aborted) throw err;
    if (err instanceof ApiError) return { error: err.code, message: err.message };
    return { error: 'network', message: 'network error' };
  }
}

export function listTitles(
  params: { kind?: ApiKind; q?: string; sort?: SortOrder; limit?: number; offset?: number },
  signal?: AbortSignal,
): Promise<TitleList> {
  return getJSON<TitleList>(`titles${query(params)}`, signal);
}

/** A title with its seasons, episodes, albums and people. Throws ApiError 404 when unknown. */
export async function getTitle(id: string, signal?: AbortSignal): Promise<TitleDetail> {
  if (!/^[1-9]\d{0,15}$/.test(id)) throw new ApiError(404, 'not_found', 'No such title.');
  return normalise(await getJSON<TitleDetail>(`titles/${id}`, signal));
}

export async function addTitle(
  input: { kind: ApiKind; provider: string; providerId: string },
  signal?: AbortSignal,
): Promise<TitleDetail> {
  const detail = await postJSON<TitleDetail>(
    'titles',
    { ...input, monitored: true },
    signal,
    ADD_TIMEOUT_MS,
  );
  return normalise(detail);
}

export async function setMonitored(id: number, monitored: boolean): Promise<TitleDetail> {
  return normalise(await patchJSON<TitleDetail>(`titles/${id}`, { monitored }));
}

export async function refreshTitle(id: number): Promise<TitleDetail> {
  return normalise(await postJSON<TitleDetail>(`titles/${id}/refresh`, undefined, undefined, ADD_TIMEOUT_MS));
}

/** The server writes empty lists as `[]`, but a page should never trip on a `null`. */
function normalise(d: TitleDetail): TitleDetail {
  return {
    ...d,
    genres: d.genres ?? [],
    altNames: d.altNames ?? [],
    seasons: d.seasons ?? [],
    episodes: d.episodes ?? [],
    albums: (d.albums ?? []).map((a) => ({ ...a, tracks: a.tracks ?? [] })),
    people: d.people ?? [],
  };
}

export interface ProviderStatus {
  lastOk: string | null;
  lastError: string;
  lastErrorAt: string | null;
}

export interface MetadataStatus {
  /** Whether a TMDB key is saved. Without one, movie lookups fail with `tmdb_key_missing`. */
  hasKey: boolean;
  providers: Partial<Record<'tmdb' | 'tvmaze' | 'musicbrainz' | 'artwork', ProviderStatus>>;
}

export function getMetadataStatus(signal?: AbortSignal): Promise<MetadataStatus> {
  return getJSON<MetadataStatus>('metadata/status', signal);
}

/* ------------------------------------------------------------------ Library */

export type FolderKind = 'movie' | 'show' | 'music';

export interface Folder {
  id: number;
  kind: FolderKind;
  path: string;
  label: string;
  createdAt: string;
  files: number;
  bytes: number;
  matched: number;
  unmatched: number;
  ignored: number;
  missing: number;
  /** null when the disk cannot be read. */
  freeBytes: number | null;
  totalBytes: number | null;
  scanning: boolean;
}

export async function getFolders(signal?: AbortSignal): Promise<Folder[]> {
  const res = await getJSON<{ folders: Folder[] | null }>('library/folders', signal);
  return res.folders ?? [];
}

export async function addFolder(input: {
  kind: FolderKind;
  path: string;
  label: string;
}): Promise<Folder> {
  const res = await postJSON<{ folder: Folder }>('library/folders', input);
  return res.folder;
}

export function removeFolder(id: number): Promise<void> {
  return del(`library/folders/${id}`);
}

export interface ScanStarted {
  started: number[];
  running: number[];
}

export function startScan(folderId?: number): Promise<ScanStarted> {
  return postJSON<ScanStarted>('library/scan', folderId ? { folderId } : undefined);
}

/** The payload of a `library.scan` event on the stream. */
export interface ScanEvent {
  folderId: number;
  state: 'started' | 'progress' | 'done' | 'failed';
  seen?: number;
  summary?: {
    seen: number;
    added: number;
    updated: number;
    missing: number;
    returned: number;
    unchanged: number;
    matched: number;
    millis: number;
  };
  error?: string;
}

export interface LibraryFile {
  id: number;
  folderId: number;
  /** Under the folder, always with "/". */
  relPath: string;
  size: number;
  mtime: string;
  kind: string;
  parsed: unknown;
  quality: string;
  titleId: number | null;
  episodeIds: number[] | null;
  trackId: number | null;
  state: 'matched' | 'unmatched' | 'ignored';
  addedAt: string;
  missingSince: string | null;
}

export interface LibraryFiles {
  files: LibraryFile[];
  total: number;
  limit: number;
  offset: number;
}

export async function getLibraryFiles(
  params: { state?: string; folderId?: number; limit?: number; offset?: number },
  signal?: AbortSignal,
): Promise<LibraryFiles> {
  const res = await getJSON<LibraryFiles>(`library/files${query(params)}`, signal);
  return { ...res, files: res.files ?? [] };
}

/* ------------------------------------------------------------------ Settings */

export interface SettingsSection {
  section: string;
  /** Secrets arrive masked ("••••1234"); send the mask back to keep the stored secret. */
  value: Record<string, unknown>;
  updatedAt: string | null;
}

export function getSettings(section: string, signal?: AbortSignal): Promise<SettingsSection> {
  return getJSON<SettingsSection>(`settings/${section}`, signal);
}

export function putSettings(
  section: string,
  value: Record<string, unknown>,
): Promise<SettingsSection> {
  return putJSON<SettingsSection>(`settings/${section}`, value);
}

/* ------------------------------------------------------------------ Requests (M4: may not exist yet) */

export type AskQuality = 'hd' | 'uhd';

export interface Usage {
  used: number;
  limit: number;
}

export interface RequestsSummary {
  waitingForYou: number;
  mine: { pending: number; approved: number };
  usage: { hd: Usage; uhd: Usage };
}

export interface AskTarget {
  titleId?: number;
  kind?: ApiKind;
  provider?: string;
  providerId?: string;
}

export interface AskAnswer {
  request: unknown;
  autoApproved: boolean;
  usage?: { hd?: Usage; uhd?: Usage };
}

export function getRequestsSummary(signal?: AbortSignal): Promise<RequestsSummary> {
  return getJSON<RequestsSummary>('requests/summary', signal);
}

export function createRequest(
  target: AskTarget,
  quality: AskQuality,
  note: string,
): Promise<AskAnswer> {
  const trimmed = note.trim();
  return postJSON<AskAnswer>('requests', {
    ...target,
    quality,
    ...(trimmed ? { note: trimmed } : {}),
  });
}

/**
 * The requests routes may not be built yet: the server then answers its generic JSON 404 (or 405
 * for a method it does not route). Pages show a quiet "not switched on" state for it.
 */
export function isRouteMissing(err: unknown): boolean {
  return err instanceof ApiError && (err.status === 404 || err.status === 405);
}

/* ------------------------------------------------------------------ Words for failures */

/** The sentence for a failed catalog call. */
export function failureKey(err: unknown): MessageKey {
  return failureKeyForCode(err instanceof ApiError ? err.code : 'network');
}

/** The sentence for one of the server's stable error codes. */
export function failureKeyForCode(code: string): MessageKey {
  switch (code) {
    case 'tmdb_key_missing':
      return 'error.tmdbKeyMissing';
    case 'tmdb_key_invalid':
      return 'error.tmdbKeyInvalid';
    case 'upstream_error':
      return 'error.upstream';
    case 'not_found':
      return 'error.notFound';
    case 'timeout':
      return 'error.timeout';
    case 'network':
      return 'error.network';
    case 'forbidden':
      return 'error.forbidden';
    default:
      return 'error.generic';
  }
}
