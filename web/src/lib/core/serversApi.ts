// The parts of the server that were still being built when the pages were wired: a title's history,
// media servers, "Open in ...", the calendar, watchlists, API keys and Claude's MCP info.
//
// Each call here may answer 404 on an older server; pages show a quiet "Not set up yet" for it
// (see `isRouteMissing` in catalog.ts). Field names follow the contracts the backend agreed to; where
// the wrapper key of a list is not certain the readers accept the likely spellings.
import { del, getJSON, postJSON, putJSON } from './api';
import { query } from './catalog';

/** The first array found under any of these keys (or the answer itself when it is an array). */
function listOf<T>(res: unknown, ...keys: string[]): T[] {
  if (Array.isArray(res)) return res as T[];
  if (res && typeof res === 'object') {
    for (const key of keys) {
      const v = (res as Record<string, unknown>)[key];
      if (Array.isArray(v)) return v as T[];
    }
  }
  return [];
}

/** The object found under any of these keys (or the answer itself). */
function oneOf<T>(res: unknown, ...keys: string[]): T {
  if (res && typeof res === 'object') {
    for (const key of keys) {
      const v = (res as Record<string, unknown>)[key];
      if (v && typeof v === 'object' && !Array.isArray(v)) return v as T;
    }
  }
  return res as T;
}

/* ------------------------------------------------------------------ History of a title */

export type HistoryEvent =
  | 'grabbed'
  | 'imported'
  | 'upgraded'
  | 'failed'
  | 'deleted'
  | 'renamed'
  | 'requested'
  | 'approved'
  | 'declined';

export interface HistoryItem {
  id?: number;
  at: string;
  event: HistoryEvent | string;
  /** A sentence, or an object with the particulars (`{by, quality, reason, ...}`). */
  detail?: unknown;
  /** The entry in plain English, written by the server ("Priya asked for 4K"). Older servers leave it out. */
  text?: string;
}

export async function getTitleHistory(titleId: number, signal?: AbortSignal): Promise<HistoryItem[]> {
  return listOf<HistoryItem>(await getJSON<unknown>(`titles/${titleId}/history`, signal), 'items', 'history');
}

/* ------------------------------------------------------------------ Media servers */

export type MediaServerKind = 'jellyfin' | 'emby' | 'plex';

export interface PathRule {
  from: string;
  to: string;
}

/** A media server as the API shows it. The token comes back masked. */
export interface MediaServer {
  id: number;
  name: string;
  kind: MediaServerKind;
  url: string;
  token: string;
  enabled: boolean;
  pathMap: PathRule[] | null;
  lastOkAt?: string | null;
  lastError?: string;
  createdAt?: string;
}

export interface MediaServerInput {
  name?: string;
  kind?: MediaServerKind;
  url?: string;
  token?: string;
  enabled?: boolean;
  pathMap?: PathRule[];
}

export interface MediaServerTest {
  ok: boolean;
  version: string;
  error: string;
  libraries: Array<{ id: string; name: string; paths: string[] }> | null;
}

export async function listMediaServers(signal?: AbortSignal): Promise<MediaServer[]> {
  return listOf<MediaServer>(await getJSON<unknown>('mediaservers', signal), 'mediaservers', 'servers', 'items');
}

export async function addMediaServer(input: MediaServerInput): Promise<MediaServer> {
  return oneOf<MediaServer>(
    await postJSON<unknown>('mediaservers', input, undefined, 30_000),
    'mediaserver',
    'server',
  );
}

export async function updateMediaServer(id: number, input: MediaServerInput): Promise<MediaServer> {
  return oneOf<MediaServer>(
    await putJSON<unknown>(`mediaservers/${id}`, input, undefined, 30_000),
    'mediaserver',
    'server',
  );
}

export function deleteMediaServer(id: number): Promise<void> {
  return del(`mediaservers/${id}`);
}

export function testMediaServer(id: number): Promise<MediaServerTest> {
  return postJSON<MediaServerTest>(`mediaservers/${id}/test`, undefined, undefined, 30_000);
}

export interface OpenTarget {
  url: string;
  server: { id: number; name: string; kind: MediaServerKind | string };
}

/** Where a title can be opened on a media server. Throws ApiError 404 when it is on none. */
export function getOpenTarget(titleId: number, signal?: AbortSignal): Promise<OpenTarget> {
  return getJSON<OpenTarget>(`titles/${titleId}/open`, signal);
}

/* ------------------------------------------------------------------ Calendar */

export type CalendarState = 'available' | 'downloading' | 'expected' | 'missing';

export interface CalendarItem {
  /** YYYY-MM-DD. */
  date: string;
  titleId: number;
  /** An episode of a show, or a movie release. */
  kind: 'episode' | 'movie' | string;
  /** The show's or the movie's name. */
  name: string;
  /** Null for a movie. */
  season: number | null;
  episode: number | null;
  episodeName: string;
  /** "S02E05" for an episode, "Release" for a movie. */
  label: string;
  state: CalendarState;
  /** 0 to 1; null unless the state is downloading. */
  progress: number | null;
  /** The reader has an ask for the title that was not declined or cancelled. */
  requestedByMe?: boolean;
}

export async function getCalendar(
  range: { from: string; to: string },
  signal?: AbortSignal,
): Promise<CalendarItem[]> {
  return listOf<CalendarItem>(await getJSON<unknown>(`calendar${query(range)}`, signal), 'items');
}

/* ------------------------------------------------------------------ Watchlists (Plex) */

export interface PlexFriend {
  plexUserId: string;
  username: string;
  linkedUserId: number | null;
  linkedUserName: string | null;
}

export interface PlexWatchlist {
  linked: boolean;
  user: string | null;
  enabled: boolean;
  includeFriends: boolean;
  autoCreateMembers?: boolean;
  /** The quality a watchlist item is asked for in. */
  quality?: 'hd' | 'uhd';
  friends: PlexFriend[] | null;
  lastCheckedAt: string | null;
  lastError?: string;
  /** What the last check did: asks made, and items skipped by reason. */
  lastResult?: { asked?: number; skipped?: Record<string, number> | null } | null;
}

export async function getWatchlists(signal?: AbortSignal): Promise<{ plex: PlexWatchlist }> {
  const res = await getJSON<{ plex: PlexWatchlist }>('watchlists', signal);
  return { plex: { ...res.plex, friends: res.plex?.friends ?? [] } };
}

export interface PlexLinkStart {
  pinId: number;
  code: string;
  authUrl: string;
}

export function startPlexLink(): Promise<PlexLinkStart> {
  return postJSON<PlexLinkStart>('watchlists/plex/link');
}

/** Poll this every 2 s: it says whether the person has approved the PIN yet. */
export function pollPlexLink(
  pinId: number | string,
  signal?: AbortSignal,
): Promise<{ linked?: boolean; user?: string | null; error?: string }> {
  return getJSON(`watchlists/plex/link/${encodeURIComponent(String(pinId))}`, signal);
}

export function unlinkPlex(): Promise<void> {
  return del('watchlists/plex/link');
}

export function linkPlexFriend(plexUserId: string, userId: number | null): Promise<void> {
  return putJSON(`watchlists/plex/friends/${encodeURIComponent(String(plexUserId))}`, { userId });
}

export function checkWatchlists(): Promise<unknown> {
  return postJSON<unknown>('watchlists/check', undefined, undefined, 60_000);
}

/** How the Plex watchlists are read. A field left out keeps its value. */
export interface PlexSettings {
  enabled?: boolean;
  includeFriends?: boolean;
  autoCreateMembers?: boolean;
  quality?: 'hd' | 'uhd';
}

/** Change how the watchlists are read (the owner). The answer is the status as it is now. */
export async function savePlexSettings(input: PlexSettings): Promise<PlexWatchlist> {
  const res = await putJSON<{ plex: PlexWatchlist }>('watchlists/plex', input);
  return { ...res.plex, friends: res.plex?.friends ?? [] };
}

/* ------------------------------------------------------------------ API keys and MCP */

export interface ApiKeyInfo {
  id: number;
  name: string;
  prefix: string;
  scopes: string[];
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

export async function listApiKeys(
  signal?: AbortSignal,
): Promise<{ keys: ApiKeyInfo[]; scopes: string[] }> {
  const res = await getJSON<{ keys: ApiKeyInfo[] | null; scopes: string[] | null }>('apikeys', signal);
  return { keys: res.keys ?? [], scopes: res.scopes ?? [] };
}

/** The secret comes back once, here. */
export function createApiKey(input: {
  name: string;
  scopes: string[];
}): Promise<{ key: ApiKeyInfo; secret: string }> {
  return postJSON<{ key: ApiKeyInfo; secret: string }>('apikeys', input);
}

export function revokeApiKey(id: number): Promise<void> {
  return del(`apikeys/${id}`);
}

export interface McpTool {
  name: string;
  /** One plain sentence for people. Older servers leave it out; `description` is written for Claude. */
  summary?: string;
  description: string;
  /** The tick a key needs to use it (see, ask, downloads, ...). */
  scope: string;
}

export interface McpCall {
  at: string;
  tool: string;
  /** Who the call ran as. */
  userName?: string;
  ok: boolean;
}

export interface McpInfo {
  enabled: boolean;
  /** The endpoint, e.g. `http://localhost:6464/api/v1/mcp`. */
  url: string;
  tools: McpTool[] | null;
  recentCalls: McpCall[] | null;
}

export async function getMcpInfo(signal?: AbortSignal): Promise<McpInfo> {
  const res = await getJSON<McpInfo>('mcp/info', signal);
  return { ...res, tools: res.tools ?? [], recentCalls: res.recentCalls ?? [] };
}

/* ------------------------------------------------------------------ Alerts */

export type AlertKind = 'discord' | 'ntfy' | 'gotify' | 'telegram' | 'pushover' | 'email' | 'webhook';

export const ALERT_KINDS: readonly AlertKind[] = [
  'discord',
  'ntfy',
  'gotify',
  'telegram',
  'pushover',
  'email',
  'webhook',
];

export const ALERT_EVENTS = [
  'request.created',
  'request.approved',
  'request.declined',
  'title.available',
  'download.failed',
  'import.failed',
  'health.problem',
] as const;

export type AlertEvent = (typeof ALERT_EVENTS)[number];

/** An alert channel as the owner sees it. Secrets in `config` come back masked. */
export interface AlertChannel {
  id: number;
  name: string;
  kind: AlertKind | string;
  config: Record<string, unknown> | null;
  events: string[] | null;
  enabled: boolean;
  lastOkAt: string | null;
  lastError: string;
  createdAt: string;
}

export interface AlertInput {
  name?: string;
  kind?: string;
  config?: Record<string, string>;
  events?: string[];
  enabled?: boolean;
}

export async function listAlerts(signal?: AbortSignal): Promise<AlertChannel[]> {
  return listOf<AlertChannel>(await getJSON<unknown>('alerts', signal), 'channels', 'alerts', 'items');
}

export async function createAlert(input: AlertInput): Promise<AlertChannel> {
  return oneOf<AlertChannel>(await postJSON<unknown>('alerts', input, undefined, 30_000), 'channel', 'alert');
}

export async function updateAlert(id: number, input: AlertInput): Promise<AlertChannel> {
  return oneOf<AlertChannel>(await putJSON<unknown>(`alerts/${id}`, input, undefined, 30_000), 'channel', 'alert');
}

export function deleteAlert(id: number): Promise<void> {
  return del(`alerts/${id}`);
}

/** Sends a test message. `ok` says whether the channel took it; `error` says why not. */
export function testAlert(id: number): Promise<{ ok?: boolean; error?: string; message?: string }> {
  return postJSON(`alerts/${id}/test`, undefined, undefined, 40_000);
}
