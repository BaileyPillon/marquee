// Sources (Torznab and Newznab feeds), quality profiles, the blocklist, release search and grab:
// `internal/web/api_sources.go`, `internal/sources`, `internal/decide`, `internal/search`.
import { del, getJSON, postJSON, putJSON } from './api';
import { query, type ApiKind } from './catalog';

export type SourceKind = 'torznab' | 'newznab';

export interface SourceCaps {
  title?: string;
  limitMax: number;
  limitDefault: number;
  modes: Record<string, { available: boolean; params?: string[] }>;
  categories: Array<{ id: number; name: string }> | null;
  fetchedAt: string;
}

/** A source (indexer) as the API shows it. The key comes back masked. */
export interface Source {
  id: number;
  name: string;
  kind: SourceKind;
  url: string;
  apiKey: string;
  categories: number[] | null;
  enabled: boolean;
  priority: number;
  caps: SourceCaps | null;
  lastOkAt: string | null;
  lastError: string;
  lastErrorAt: string | null;
  createdAt: string;
}

export interface SourceInput {
  name?: string;
  kind?: SourceKind;
  url?: string;
  apiKey?: string;
  categories?: number[];
  enabled?: boolean;
  priority?: number;
}

export interface SourceTest {
  ok: boolean;
  /** A stable word for what is wrong (`unauthorized`, `timeout`, ...); empty when it works. */
  code: string;
  message: string;
  /** How long the source took to answer, in milliseconds. */
  ms: number;
  caps: SourceCaps | null;
  source: Source;
}

export async function listSources(signal?: AbortSignal): Promise<Source[]> {
  return (await getJSON<{ sources: Source[] | null }>('sources', signal)).sources ?? [];
}

export async function createSource(input: SourceInput): Promise<Source> {
  return (await postJSON<{ source: Source }>('sources', input, undefined, 30_000)).source;
}

export async function updateSource(id: number, input: SourceInput): Promise<Source> {
  return (await putJSON<{ source: Source }>(`sources/${id}`, input, undefined, 30_000)).source;
}

export function deleteSource(id: number): Promise<void> {
  return del(`sources/${id}`);
}

export function testSource(id: number): Promise<SourceTest> {
  return postJSON<SourceTest>(`sources/${id}/test`, undefined, undefined, 40_000);
}

export interface ImportResult {
  sources: Source[];
  skipped: Array<{ name: string; reason?: string } | string> | null;
}

export async function importSources(input: {
  from: 'prowlarr' | 'jackett';
  url: string;
  apiKey: string;
}): Promise<ImportResult> {
  const res = await postJSON<ImportResult>('sources/import', input, undefined, 60_000);
  return { sources: res.sources ?? [], skipped: res.skipped ?? [] };
}

/* ------------------------------------------------------------------ Quality profiles */

export interface Profile {
  id: number;
  name: string;
  kind: ApiKind;
  /** "hd" or "uhd". */
  quality: string;
  allowedRes: number[] | null;
  preferredRes: number;
  allowedSources: string[] | null;
  minSizeMb: number | null;
  maxSizeMb: number | null;
  maxSizePerHourMb: number | null;
  preferHdr: boolean;
  preferredWords: string[] | null;
  rejectedWords: string[] | null;
  minSeeders: number;
  upgradeUntil: string | null;
  createdAt: string;
}

/** What `PUT /profiles/{id}` takes: a field left out keeps its value, a size sent as null is cleared. */
export type ProfileInput = Partial<
  Pick<
    Profile,
    | 'name'
    | 'allowedRes'
    | 'preferredRes'
    | 'allowedSources'
    | 'minSizeMb'
    | 'maxSizeMb'
    | 'maxSizePerHourMb'
    | 'preferHdr'
    | 'preferredWords'
    | 'rejectedWords'
    | 'minSeeders'
    | 'upgradeUntil'
  >
>;

export async function listProfiles(signal?: AbortSignal): Promise<Profile[]> {
  return (await getJSON<{ profiles: Profile[] | null }>('profiles', signal)).profiles ?? [];
}

export async function updateProfile(id: number, input: ProfileInput): Promise<Profile> {
  return (await putJSON<{ profile: Profile }>(`profiles/${id}`, input)).profile;
}

/** The resolutions and sources a profile may allow (from `internal/decide/profile.go`). */
export const RESOLUTIONS = [480, 576, 720, 1080, 2160] as const;
export const VIDEO_SOURCES = [
  'Remux',
  'BluRay',
  'WEB-DL',
  'WEBRip',
  'HDTV',
  'SDTV',
  'DVD',
  'Raw-HD',
  'Unknown',
] as const;
export const MUSIC_FORMATS = ['FLAC', 'MP3-320', 'MP3-V0', 'MP3-256', 'MP3-192', 'MP3-128', 'AAC', 'Opus'] as const;

export interface BlocklistEntry {
  id: number;
  titleId: number;
  guid: string;
  infoHash: string;
  releaseTitle: string;
  reason: string;
  createdAt: string;
}

export async function listBlocklist(
  signal?: AbortSignal,
): Promise<{ items: BlocklistEntry[]; total: number }> {
  const res = await getJSON<{ items: BlocklistEntry[] | null; total: number }>('blocklist', signal);
  return { items: res.items ?? [], total: res.total };
}

export function deleteBlocklistEntry(id: number): Promise<void> {
  return del(`blocklist/${id}`);
}

/* ------------------------------------------------------------------ Releases and grab */

export interface FoundRelease {
  indexer: { id: number; name: string };
  guid: string;
  title: string;
  /** Bytes. */
  size: number;
  protocol: 'torrent' | 'usenet';
  seeders: number | null;
  /** Whole days; null when the source gave no date. */
  age: number | null;
  publishedAt: string | null;
  /** The parsed quality name, e.g. "Bluray-2160p". */
  quality: string;
  score: number;
  accepted: boolean;
  /** Plain-words reasons for the score. */
  reasons: string[];
  /** Why a release was turned down; empty when accepted. */
  rejection: string;
}

export interface ReleaseSearch {
  results: FoundRelease[];
  searchedAt: string;
  indexersSearched?: number;
  indexersFailed: Array<{ id: number; name: string; error: string }>;
}

/** Asking every source can take a while; the server gives up on its own. */
const RELEASE_SEARCH_TIMEOUT_MS = 90_000;

export async function findReleases(
  titleId: number,
  params: { quality: 'hd' | 'uhd'; season?: number; episode?: number },
  signal?: AbortSignal,
): Promise<ReleaseSearch> {
  const res = await getJSON<ReleaseSearch>(
    `titles/${titleId}/releases${query(params)}`,
    signal,
    RELEASE_SEARCH_TIMEOUT_MS,
  );
  return {
    ...res,
    results: res.results ?? [],
    indexersFailed: res.indexersFailed ?? [],
  };
}

export interface GrabAnswer {
  downloadId: number;
  release: { title: string; quality: string; score: number; reasons: string[] };
}

export function grabRelease(
  titleId: number,
  input: { indexerId: number; guid: string; quality: 'hd' | 'uhd' },
): Promise<GrabAnswer> {
  return postJSON<GrabAnswer>(`titles/${titleId}/grab`, input, undefined, 30_000);
}
