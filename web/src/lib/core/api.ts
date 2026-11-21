// API seam: every call to the Go backend goes through here.
const BASE = '/api/v1';
const TIMEOUT_MS = 5000;

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

/** GET `/api/v1/<path>` and parse the JSON body. Throws ApiError on any failure. */
export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const url = `${BASE}/${path.replace(/^\/+/, '')}`;
  const timeout = AbortSignal.timeout(TIMEOUT_MS);
  let res: Response;
  try {
    res = await fetch(url, {
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
  } catch (err) {
    throw new ApiError(0, err instanceof Error ? err.message : 'network error');
  }
  if (!res.ok) throw new ApiError(res.status, res.statusText || `HTTP ${res.status}`);
  try {
    return (await res.json()) as T;
  } catch {
    // A static server that falls back to index.html answers 200 with HTML.
    throw new ApiError(res.status, 'response was not JSON');
  }
}

export interface Health {
  status: string;
  version?: string;
}

/** GET `/api/v1/health`. Resolves when the server is up, rejects when it is not. */
export function health(signal?: AbortSignal): Promise<Health> {
  return getJSON<Health>('/health', signal);
}
