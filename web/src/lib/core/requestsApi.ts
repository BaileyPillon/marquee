// Asks, answers, people and limits: `internal/web/api_requests.go` and `internal/requests`.
// Field names follow the Go structs exactly.
import { del, getJSON, postJSON, putJSON } from './api';
import { query, type ApiKind, type AskQuality, type Usage } from './catalog';

export type RequestState =
  | 'pending'
  | 'approved'
  | 'declined'
  | 'available'
  | 'partial'
  | 'failed'
  | 'cancelled';

/** Where an ask came from. */
export type RequestSource = 'manual' | 'watchlist' | 'mcp' | 'list';

export interface UserRef {
  id: number;
  name: string;
}

export interface RequestTitle {
  id: number;
  name: string;
  year: number;
  kind: ApiKind;
  posterRef: string;
}

/** How much of a title was asked for: `{}` for a movie, seasons or albums, or `{all:true}`. */
export interface RequestScope {
  all?: boolean;
  seasons?: number[];
  albums?: number[];
}

export interface RequestItem {
  id: number;
  title: RequestTitle;
  /** Who asked. Null when the person is hidden from the reader. */
  user: UserRef | null;
  quality: AskQuality;
  scope: RequestScope;
  note: string;
  state: RequestState;
  source: RequestSource;
  createdAt: string;
  updatedAt: string;
  /** Null while waiting, and when the rules approved it. */
  decidedBy: UserRef | null;
  decidedAt: string | null;
  declineReason: string;
  facts: {
    /** The expected size in bytes; null until sources can say. */
    sizeEstimate: number | null;
    /** Where the copy goes (or would go, while the ask waits). */
    folderPath: string;
  };
}

export interface RequestList {
  items: RequestItem[];
  total: number;
  limit: number;
  offset: number;
}

/** The server writes an empty list as `[]` but a page should never trip on `null`. */
function normaliseList(list: RequestList): RequestList {
  return { ...list, items: list.items ?? [] };
}

export async function listRequests(
  params: { state?: string; mine?: boolean; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<RequestList> {
  const { mine, ...rest } = params;
  const res = await getJSON<RequestList>(
    `requests${query({ ...rest, ...(mine ? { mine: 1 } : {}) })}`,
    signal,
  );
  return normaliseList(res);
}

export async function approveRequest(
  id: number,
  options: { alwaysAllowUHD?: boolean } = {},
): Promise<RequestItem> {
  const res = await postJSON<{ request: RequestItem }>(
    `requests/${id}/approve`,
    options.alwaysAllowUHD ? { alwaysAllowUHD: true } : {},
  );
  return res.request;
}

export async function declineRequest(id: number, reason: string): Promise<RequestItem> {
  const trimmed = reason.trim();
  const res = await postJSON<{ request: RequestItem }>(
    `requests/${id}/decline`,
    trimmed ? { reason: trimmed } : {},
  );
  return res.request;
}

export async function cancelRequest(id: number): Promise<RequestItem> {
  const res = await del<{ request: RequestItem }>(`requests/${id}`);
  return res.request;
}

/* ------------------------------------------------------------------ People and limits */

export interface PersonLimits {
  effective: {
    /** null: no limit. */
    hdPerWeek: number | null;
    uhdPerWeek: number | null;
    autoApproveHD: boolean;
    autoApproveUHD: boolean;
  };
  /** What the owner set by name; null means "use the default". */
  overrides: {
    hdPerWeek: number | null;
    uhdPerWeek: number | null;
    autoApproveHD: boolean | null;
    autoApproveUHD: boolean | null;
  };
}

export interface PersonInfo {
  id: number;
  name: string;
  role: 'owner' | 'admin' | 'member';
  disabled: boolean;
  limits: PersonLimits;
  usage: { hd: Usage; uhd: Usage };
  autoApprove: { hd: boolean; uhd: boolean };
}

export interface RequestDefaults {
  hdPerWeek: number;
  uhdPerWeek: number;
  autoApproveHD: boolean;
  autoApproveUHD: boolean;
}

export interface PeopleResult {
  people: PersonInfo[];
  defaults: RequestDefaults;
}

export async function getPeople(signal?: AbortSignal): Promise<PeopleResult> {
  const res = await getJSON<PeopleResult>('people', signal);
  return { ...res, people: res.people ?? [] };
}

/** A field left out stays; a field sent as null goes back to the default. */
export interface LimitsPatch {
  hdPerWeek?: number | null;
  uhdPerWeek?: number | null;
  autoApproveHD?: boolean | null;
  autoApproveUHD?: boolean | null;
}

export async function setLimits(userId: number, patch: LimitsPatch): Promise<PersonInfo> {
  const res = await putJSON<{ person: PersonInfo }>(`people/${userId}/limits`, patch);
  return res.person;
}
