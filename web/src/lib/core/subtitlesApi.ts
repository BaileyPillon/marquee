// Subtitles: the settings section, a title's files with their subtitles, and the manual search.
//
// The server never sends a path or a download link; a result is picked by its provider and id.
import { del, getJSON, postJSON } from './api';
import { getSettings, putSettings } from './catalog';

export type HearingImpaired = 'prefer-not' | 'prefer' | 'only';

export interface OpenSubtitlesSettings {
  enabled: boolean;
  /** Masked ("••••1234") when it comes from the server; send the mask back to keep the secret. */
  apiKey: string;
  username: string;
  /** Masked like the key. */
  password: string;
}

export interface SubtitleSettings {
  languages: string[];
  hearingImpaired: HearingImpaired;
  minScore: number;
  searchAfterImport: boolean;
  rescanEveryHours: number;
  opensubtitles: OpenSubtitlesSettings;
  podnapisi: { enabled: boolean };
}

export const DEFAULT_SUBTITLE_SETTINGS: SubtitleSettings = {
  languages: ['en'],
  hearingImpaired: 'prefer-not',
  minScore: 60,
  searchAfterImport: true,
  rescanEveryHours: 24,
  opensubtitles: { enabled: false, apiKey: '', username: '', password: '' },
  podnapisi: { enabled: true },
};

function text(v: unknown, fallback = ''): string {
  return typeof v === 'string' ? v : fallback;
}

function flag(v: unknown, fallback: boolean): boolean {
  return typeof v === 'boolean' ? v : fallback;
}

function whole(v: unknown, fallback: number): number {
  return typeof v === 'number' && Number.isFinite(v) ? Math.round(v) : fallback;
}

/** Read the `subtitles` section as the form uses it: whatever the owner never set takes its default. */
export async function loadSubtitleSettings(signal?: AbortSignal): Promise<SubtitleSettings> {
  const section = await getSettings('subtitles', signal);
  const v = section.value;
  const d = DEFAULT_SUBTITLE_SETTINGS;
  const providers = (typeof v['providers'] === 'object' && v['providers'] !== null ? v['providers'] : {}) as Record<
    string,
    Record<string, unknown> | undefined
  >;
  const os = providers['opensubtitles'] ?? {};
  const pod = providers['podnapisi'] ?? {};
  const hi = v['hearingImpaired'];
  return {
    languages: Array.isArray(v['languages']) ? v['languages'].filter((l): l is string => typeof l === 'string') : [...d.languages],
    hearingImpaired: hi === 'prefer' || hi === 'only' || hi === 'prefer-not' ? hi : d.hearingImpaired,
    minScore: whole(v['minScore'], d.minScore),
    searchAfterImport: flag(v['searchAfterImport'], d.searchAfterImport),
    rescanEveryHours: whole(v['rescanEveryHours'], d.rescanEveryHours),
    opensubtitles: {
      enabled: flag(os['enabled'], d.opensubtitles.enabled),
      apiKey: text(os['apiKey']),
      username: text(os['username']),
      password: text(os['password']),
    },
    podnapisi: { enabled: flag(pod['enabled'], d.podnapisi.enabled) },
  };
}

/** Save the whole section. A key or password still masked keeps the stored one. */
export async function saveSubtitleSettings(s: SubtitleSettings): Promise<SubtitleSettings> {
  await putSettings('subtitles', {
    languages: s.languages,
    hearingImpaired: s.hearingImpaired,
    minScore: s.minScore,
    searchAfterImport: s.searchAfterImport,
    rescanEveryHours: s.rescanEveryHours,
    providers: {
      opensubtitles: {
        enabled: s.opensubtitles.enabled,
        apiKey: s.opensubtitles.apiKey.trim(),
        username: s.opensubtitles.username.trim(),
        password: s.opensubtitles.password,
      },
      podnapisi: { enabled: s.podnapisi.enabled },
    },
  });
  return loadSubtitleSettings();
}

/* ------------------------------------------------------------------ status and test */

export interface SubtitleProviderStatus {
  name: 'opensubtitles' | 'podnapisi' | string;
  enabled: boolean;
  ok: boolean;
  quotaRemaining?: number;
  error?: string;
  blockedUntil?: string;
}

export interface SubtitleStatus {
  providers: SubtitleProviderStatus[];
  missingCount: number;
  languages: string[];
}

export function getSubtitleStatus(signal?: AbortSignal): Promise<SubtitleStatus> {
  return getJSON<SubtitleStatus>('subtitles/status', signal, 15000);
}

/** Try a provider with what is typed in the form (an empty or masked field uses the saved one). */
export function testSubtitleProvider(
  provider: 'opensubtitles' | 'podnapisi',
  fields: { apiKey?: string; username?: string; password?: string } = {},
): Promise<{ ok: boolean }> {
  return postJSON<{ ok: boolean }>('subtitles/test', { provider, ...fields }, undefined, 30000);
}

/* ------------------------------------------------------------------ a title's files */

export interface FileSubtitle {
  /** Set for a file Marquee saved (it can be removed); absent for one that was already there. */
  id?: number;
  language: string;
  hi: boolean;
  forced?: boolean;
  provider?: string;
  score?: number;
  /** The subtitle file's name, never its folder. */
  name: string;
  source: 'marquee' | 'file';
  addedAt?: string;
}

export interface FileSubtitles {
  fileId: number;
  /** The languages the owner wants. */
  languages: string[];
  subtitles: FileSubtitle[];
  missing: string[];
  /** The video is on disk and a provider is set up. */
  canSearch: boolean;
}

export interface TitleSubtitleFile extends FileSubtitles {
  name: string;
  episodeIds: number[];
}

export async function getTitleSubtitles(titleId: number | string, signal?: AbortSignal): Promise<TitleSubtitleFile[]> {
  const res = await getJSON<{ files: TitleSubtitleFile[] }>(`titles/${titleId}/subtitles`, signal, 15000);
  return res.files ?? [];
}

/* ------------------------------------------------------------------ the manual search */

export interface SubtitleResult {
  provider: string;
  providerId: string;
  language: string;
  release: string;
  releases: string[];
  hi: boolean;
  downloads: number;
  hashMatch: boolean;
  score: number;
  /** The score meets the owner's minimum, so an automatic search would take it. */
  accepted: boolean;
  reasons: string[];
}

export interface SubtitleProviderOutcome {
  name: string;
  ok: boolean;
  error?: string;
  skipped?: boolean;
}

export interface SubtitleSearch {
  language: string;
  minScore: number;
  results: SubtitleResult[];
  providers: SubtitleProviderOutcome[];
}

export function searchSubtitles(fileId: number, language: string, signal?: AbortSignal): Promise<SubtitleSearch> {
  return postJSON<SubtitleSearch>(`library/files/${fileId}/subtitles/search`, { language }, signal, 60000);
}

export interface SavedSubtitle {
  id: number;
  fileId: number;
  language: string;
  hi: boolean;
  provider: string;
  name: string;
}

export async function downloadSubtitle(fileId: number, result: SubtitleResult): Promise<SavedSubtitle> {
  const res = await postJSON<{ subtitle: SavedSubtitle }>(
    `library/files/${fileId}/subtitles/download`,
    { provider: result.provider, providerId: result.providerId, language: result.language },
    undefined,
    60000,
  );
  return res.subtitle;
}

export function removeSubtitle(id: number): Promise<void> {
  return del(`subtitles/${id}`);
}

/* ------------------------------------------------------------------ the album search */

export interface AlbumSearchResult {
  grabbed: boolean;
  found: number;
  downloadId?: number;
  release?: { title: string; quality: string; score: number; reasons: string[] };
  note?: string;
}

/** Look for one album now and send the best acceptable release to the download app. */
export function searchAlbum(titleId: number, albumId: number): Promise<AlbumSearchResult> {
  return postJSON<AlbumSearchResult>(`titles/${titleId}/albums/${albumId}/search`, undefined, undefined, 90000);
}
