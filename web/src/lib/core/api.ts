// API seam: every call to the Go backend goes through here.
//
// Errors come back from the server as {"error":{"code","message"}} and are thrown as ApiError.
// Every request that is not a GET carries `X-Marquee: 1`: the server refuses anything else with
// 403 `csrf` (a browser cannot add a custom header to a cross-site form post).
const BASE = '/api/v1';
const READ_TIMEOUT_MS = 5000;
const WRITE_TIMEOUT_MS = 15000;
const CSRF_HEADER = 'X-Marquee';

/** What went wrong in a call. `status` is 0 when no HTTP answer came back (offline, timeout). */
export class ApiError extends Error {
  readonly status: number;
  /** The server's machine code (`invalid_credentials`), or `network`, `timeout`, `bad_response`. */
  readonly code: string;
  /** Seconds to wait before trying again (from a `Retry-After` header), when the server sent one. */
  readonly retryAfter?: number;

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    if (retryAfter !== undefined) this.retryAfter = retryAfter;
  }
}

let unauthorizedHandler: (() => void) | null = null;

/**
 * Register what happens when any call comes back 401 (the session ended or never existed). The
 * session store sets this to "go to the sign-in page". A wrong password is also a 401
 * (`invalid_credentials`) but it is a form error, not a lost session, so it does not fire it.
 */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler;
}

function url(path: string): string {
  return `${BASE}/${path.replace(/^\/+/, '')}`;
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds);
  const at = Date.parse(value);
  if (Number.isNaN(at)) return undefined;
  return Math.max(0, Math.ceil((at - Date.now()) / 1000));
}

async function toApiError(res: Response): Promise<ApiError> {
  let code = `http_${res.status}`;
  let message = res.statusText || `HTTP ${res.status}`;
  try {
    const body = (await res.json()) as { error?: { code?: unknown; message?: unknown } };
    if (typeof body.error?.code === 'string') code = body.error.code;
    if (typeof body.error?.message === 'string') message = body.error.message;
  } catch {
    // Not a JSON error body (a proxy page, say); keep the HTTP status text.
  }
  return new ApiError(res.status, code, message, parseRetryAfter(res.headers.get('Retry-After')));
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

async function request<T>(
  method: Method,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (method !== 'GET') headers[CSRF_HEADER] = '1';
  let payload: string | undefined;
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }

  const timeout = AbortSignal.timeout(method === 'GET' ? READ_TIMEOUT_MS : WRITE_TIMEOUT_MS);
  let res: Response;
  try {
    res = await fetch(url(path), {
      method,
      headers,
      credentials: 'same-origin',
      ...(payload !== undefined ? { body: payload } : {}),
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
  } catch (err) {
    if (timeout.aborted) throw new ApiError(0, 'timeout', 'The server took too long to answer.');
    throw new ApiError(0, 'network', err instanceof Error ? err.message : 'network error');
  }

  if (!res.ok) {
    const error = await toApiError(res);
    if (res.status === 401 && error.code !== 'invalid_credentials') unauthorizedHandler?.();
    throw error;
  }
  if (res.status === 204) return undefined as T;
  try {
    return (await res.json()) as T;
  } catch {
    // A static server that falls back to index.html answers 200 with HTML.
    throw new ApiError(res.status, 'bad_response', 'response was not JSON');
  }
}

/** GET `/api/v1/<path>` and parse the JSON body. Throws ApiError on any failure. */
export function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  return request<T>('GET', path, undefined, signal);
}

/** POST a JSON body (or none). Resolves with the parsed answer, or undefined on 204. */
export function postJSON<T = void>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return request<T>('POST', path, body, signal);
}

/** PUT a JSON body. */
export function putJSON<T = void>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return request<T>('PUT', path, body, signal);
}

/** PATCH a JSON body. */
export function patchJSON<T = void>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return request<T>('PATCH', path, body, signal);
}

/** DELETE `/api/v1/<path>`; a JSON body is optional. */
export function del<T = void>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return request<T>('DELETE', path, body, signal);
}

export interface Health {
  status: string;
  version?: string;
}

/** GET `/api/v1/health`. Resolves when the server is up, rejects when it is not. */
export function health(signal?: AbortSignal): Promise<Health> {
  return getJSON<Health>('/health', signal);
}

/** Is the server there? Never throws: true when `/health` answers, false for any failure. */
export async function ping(signal?: AbortSignal): Promise<boolean> {
  try {
    await health(signal);
    return true;
  } catch {
    return false;
  }
}
