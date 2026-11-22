// How Marquee is doing and how it is reached: `internal/web/api_system.go` and the "server" settings
// section (`internal/web/serverconfig.go`).
import { getJSON, putJSON } from './api';

export interface DatabaseStatus {
  /** "main" or "events". */
  name: string;
  file: string;
  ok: boolean;
  /** The file and its write-ahead log. */
  sizeBytes: number;
}

export interface JobStatus {
  name: string;
  intervalSeconds: number;
  running: boolean;
  runs: number;
  failures: number;
  /** Null until the job has run. */
  lastRun: string | null;
  lastDurationMs: number;
  lastError: string;
}

/** One app Marquee talks to and how the last call went. */
export interface ConnectionStatus {
  kind: 'downloadClient' | 'source' | 'mediaServer' | 'metadata' | 'alertChannel' | 'plexWatchlist' | string;
  id?: number;
  name: string;
  /** qbittorrent, torznab, jellyfin, tvmaze, ... */
  app: string;
  ok: boolean;
  lastError: string;
  lastOkAt: string | null;
}

export interface SystemStatus {
  version: string;
  go: string;
  os: string;
  arch: string;
  startedAt: string;
  uptimeSeconds: number;
  configDir: string;
  databases: DatabaseStatus[];
  /** Null when the config folder's disk cannot be read. */
  disk: { path: string; freeBytes: number; totalBytes: number } | null;
  jobs: JobStatus[];
  connections: ConnectionStatus[];
  server: { publicUrl: string; trustedProxies: string[]; trustedFromEnvironment: string[] };
  repository: string;
  license: string;
}

/** The owner's and admins' view of how Marquee is doing. */
export async function getSystemStatus(signal?: AbortSignal): Promise<SystemStatus> {
  const res = await getJSON<SystemStatus>('system/status', signal);
  return {
    ...res,
    databases: res.databases ?? [],
    jobs: res.jobs ?? [],
    connections: res.connections ?? [],
    server: {
      publicUrl: res.server?.publicUrl ?? '',
      trustedProxies: res.server?.trustedProxies ?? [],
      trustedFromEnvironment: res.server?.trustedFromEnvironment ?? [],
    },
  };
}

/** The "server" settings section (the owner). */
export interface ServerSettings {
  /** The address people use from outside, such as https://marquee.example.com; empty when not set. */
  publicUrl: string;
  /** Reverse proxies whose X-Forwarded-For and X-Forwarded-Proto headers are believed (CIDR or address). */
  trustedProxies: string[];
}

interface ServerSection {
  value: { publicUrl?: unknown; trustedProxies?: unknown };
}

function readServer(res: ServerSection): ServerSettings {
  const v = res.value ?? {};
  return {
    publicUrl: typeof v.publicUrl === 'string' ? v.publicUrl : '',
    trustedProxies: Array.isArray(v.trustedProxies)
      ? v.trustedProxies.filter((p): p is string => typeof p === 'string')
      : [],
  };
}

export async function getServerSettings(signal?: AbortSignal): Promise<ServerSettings> {
  return readServer(await getJSON<ServerSection>('settings/server', signal));
}

export async function saveServerSettings(input: ServerSettings): Promise<ServerSettings> {
  return readServer(await putJSON<ServerSection>('settings/server', input));
}
