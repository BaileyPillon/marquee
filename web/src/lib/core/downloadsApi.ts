// Downloads and the download apps ("clients"): `internal/web/api_downloads.go`, `internal/downloads`.
// Field names follow the Go structs exactly.
import { del, getJSON, postJSON, putJSON } from './api';
import { getSettings, putSettings, query, type ApiKind } from './catalog';

/** queued -> downloading (or paused, stalled) -> completed -> importing -> imported; or failed, removed. */
export type DownloadState =
  | 'queued'
  | 'downloading'
  | 'paused'
  | 'stalled'
  | 'completed'
  | 'importing'
  | 'imported'
  | 'failed'
  | 'removed';

export interface DownloadView {
  id: number;
  /** Null when the title has since been deleted. */
  title: { id: number; name: string; year: number; kind: ApiKind; posterRef: string } | null;
  client: { id: number; name: string; kind: string };
  copyId: number | null;
  protocol: 'torrent' | 'usenet';
  kind: string;
  /** "hd" or "uhd" for a copy; a parsed name such as "Bluray-1080p" otherwise. */
  quality: string;
  episodeIds: number[] | null;
  releaseTitle: string;
  state: DownloadState;
  /** 0 to 1. */
  progress: number;
  speedBps: number;
  /** Null when unknown. */
  etaSeconds: number | null;
  size: number;
  error: string;
  createdAt: string;
  updatedAt: string;
  completedAt: string | null;
  /**
   * Where the import is up to while the download is importing (and after): checking, renaming,
   * moving, rescanning, or done. Empty before an import starts. Older servers leave it out.
   */
  importStep?: ImportStep | '';
  /** The path the import is putting the files in (owner and admins only). */
  importTarget?: string;
  /** How a finished download ended; empty while it is going. Older servers leave it out. */
  outcome?: DownloadOutcome | '';
}

/** The steps of an import, in order. */
export type ImportStep = 'checking' | 'renaming' | 'moving' | 'rescanning' | 'done';

export const IMPORT_STEPS: readonly ImportStep[] = ['checking', 'renaming', 'moving', 'rescanning', 'done'];

/** How a finished download ended (the history list). */
export type DownloadOutcome = 'imported' | 'upgraded' | 'failed' | 'removed';

export interface DownloadList {
  downloads: DownloadView[];
  total: number;
  limit: number;
  offset: number;
}

export type DownloadFilter = 'active' | 'history' | 'all';

export async function listDownloads(
  params: { state?: DownloadFilter; limit?: number; offset?: number },
  signal?: AbortSignal,
): Promise<DownloadList> {
  const res = await getJSON<DownloadList>(`downloads${query(params)}`, signal);
  return { ...res, downloads: res.downloads ?? [] };
}

export interface ClientHealth {
  id: number;
  name: string;
  kind: string;
  ok: boolean;
  lastError: string;
}

export interface DownloadsSummary {
  downloading: number;
  importing: number;
  queued: number;
  clients: ClientHealth[];
}

export async function getDownloadsSummary(signal?: AbortSignal): Promise<DownloadsSummary> {
  const res = await getJSON<DownloadsSummary>('downloads/summary', signal);
  return { ...res, clients: res.clients ?? [] };
}

export async function pauseDownload(id: number): Promise<DownloadView> {
  return (await postJSON<{ download: DownloadView }>(`downloads/${id}/pause`)).download;
}

export async function resumeDownload(id: number): Promise<DownloadView> {
  return (await postJSON<{ download: DownloadView }>(`downloads/${id}/resume`)).download;
}

/** The download leaves its client and stays in the history as "removed". */
export function removeDownload(id: number, deleteData: boolean): Promise<void> {
  return del(`downloads/${id}${query({ deleteData: deleteData ? 'true' : undefined })}`);
}

/** The payload of a `downloads` event. Progress arrives batched: `{type:"progress", items:[...]}`. */
export interface DownloadEvent {
  type: 'added' | 'state' | 'completed' | 'failed' | 'removed' | 'progress';
  id?: number;
  titleId?: number;
  state?: DownloadState;
  progress?: number;
  /** On a `state` event: where the import is up to. (Never the path: the stream reaches everyone.) */
  importStep?: ImportStep | '';
  items?: Array<{
    id: number;
    state: DownloadState;
    progress: number;
    speedBps: number;
    etaSeconds: number | null;
  }>;
}

/** Is the download still going: not yet imported, failed or removed? */
export function isOpenState(state: DownloadState): boolean {
  return state !== 'imported' && state !== 'failed' && state !== 'removed';
}

/* ------------------------------------------------------------------ Download apps */

export type ClientKind = 'qbittorrent' | 'deluge' | 'sabnzbd';

/** A download app as the API shows it. Secrets come back masked ("••••1234"). */
export interface DownloadClient {
  id: number;
  name: string;
  kind: ClientKind | string;
  url: string;
  username: string;
  password: string;
  apiKey: string;
  category: string;
  savePath: string;
  enabled: boolean;
  priority: number;
  lastOkAt: string | null;
  lastError: string;
  lastErrorAt: string | null;
  createdAt: string;
}

/** What the owner sends. A field left out keeps its value; a masked secret keeps the stored one. */
export interface ClientInput {
  name?: string;
  kind?: string;
  url?: string;
  username?: string;
  password?: string;
  apiKey?: string;
  category?: string;
  savePath?: string;
  enabled?: boolean;
  priority?: number;
}

export interface TestResult {
  ok: boolean;
  version: string;
  error: string;
}

export async function listClients(signal?: AbortSignal): Promise<DownloadClient[]> {
  return (await getJSON<{ clients: DownloadClient[] | null }>('clients', signal)).clients ?? [];
}

export async function addClient(input: ClientInput): Promise<DownloadClient> {
  return (await postJSON<{ client: DownloadClient }>('clients', input, undefined, 30_000)).client;
}

export async function updateClient(id: number, input: ClientInput): Promise<DownloadClient> {
  return (await putJSON<{ client: DownloadClient }>(`clients/${id}`, input, undefined, 30_000)).client;
}

export function deleteClient(id: number): Promise<void> {
  return del(`clients/${id}`);
}

/** Always answers 200: `ok` says whether the app answered, `error` says why not. */
export function testClient(id: number): Promise<TestResult> {
  return postJSON<TestResult>(`clients/${id}/test`, undefined, undefined, 30_000);
}

/* ------------------------------------------------------------------ when to give up */

/** The `downloads` settings section: what Marquee does with a download that stops making progress. */
export interface DownloadSettings {
  /** Minutes without progress before Marquee gives up on a download; 0 never gives up. */
  stallMinutes: number;
  /** Also remove a download it gave up on from the download app, with what it had downloaded. */
  removeStalled: boolean;
}

export const DEFAULT_DOWNLOAD_SETTINGS: DownloadSettings = { stallMinutes: 30, removeStalled: true };

/** The longest wait the server takes: a week. */
export const MAX_STALL_MINUTES = 7 * 24 * 60;

/** Read the `downloads` section as the form uses it: whatever the owner never set takes its default. */
export async function loadDownloadSettings(signal?: AbortSignal): Promise<DownloadSettings> {
  const v = (await getSettings('downloads', signal)).value;
  const d = DEFAULT_DOWNLOAD_SETTINGS;
  const minutes = v['stallMinutes'];
  const remove = v['removeStalled'];
  return {
    stallMinutes: typeof minutes === 'number' && Number.isFinite(minutes) ? Math.round(minutes) : d.stallMinutes,
    removeStalled: typeof remove === 'boolean' ? remove : d.removeStalled,
  };
}

export async function saveDownloadSettings(s: DownloadSettings): Promise<DownloadSettings> {
  await putSettings('downloads', { stallMinutes: s.stallMinutes, removeStalled: s.removeStalled });
  return loadDownloadSettings();
}
