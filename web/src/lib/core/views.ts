// Shapes of the sample content that pages still show while their backends are not built (requests,
// downloads, the Connections panels, a movie's releases and history), plus a few shared pieces. The
// mock modules in lib/mock/ build these; the Go API will send the same shapes later, so a page
// never knows which one answered. What the catalog and library APIs send is in catalog.ts.
//
// Strings in these types are content (names, dates, sizes, reasons the server wrote). Interface
// text lives in en.json and goes through t().

/** One label and value pair, as the server describes a request, a rule or a limit. */
export interface Fact {
  label: string;
  value: string;
}

/* ------------------------------------------------------------------ Movie page (sample sections) */

export type HistoryKind = 'found' | 'asked' | 'imported' | 'grabbed';

export interface Release {
  name: string;
  size: string;
  source: string;
  age: string;
  score: number;
  /** The plain-words reason the server gives for this score. */
  reason: string;
  best: boolean;
}

/* ------------------------------------------------------------------ Show page */

/** How an episode stands: unknown when the library's file list is not available to this person. */
export type EpisodeState = 'library' | 'downloading' | 'missing' | 'upcoming' | 'unknown';

/* ------------------------------------------------------------------ Requests */

export interface WaitingRequest {
  id: string;
  /** Who asked. */
  requester: string;
  /** Who and when, e.g. "Priya · today 08:30". */
  who: string;
  /** How long ago, for the phone card. */
  ago: string;
  title: string;
  year: number;
  /** Year line on the small poster, e.g. "1939 · TV". */
  posterYear: string;
  /** Year and kind on the phone card, e.g. "1927 · Film". */
  posterMeta: string;
  quality: 'HD' | '4K';
  what: string;
  /** Short line under the title in the phone's "Also waiting" list. */
  brief: string;
  facts: Fact[];
  phoneFacts: Fact[];
}

export interface DecidedRequest {
  id: string;
  title: string;
  detail: string;
  outcome: 'approved' | 'declined';
  when: string;
  fromWatchlist: boolean;
}

export interface Person {
  name: string;
  hd: string;
  fourK: string;
  autoApprove: boolean;
}

export interface RequestsView {
  /** Asks decided in the last 7 days, before any decision made on this screen. */
  decidedThisWeek: number;
  waiting: WaitingRequest[];
  decided: DecidedRequest[];
  people: Person[];
  watchlists: { headline: string; detail: string };
}

export type RequestDecision = 'approved' | 'declined';

/* ------------------------------------------------------------------ Downloads */

export type DownloadKind = 'downloading' | 'importing' | 'queued';

export interface DownloadItem {
  id: string;
  title: string;
  quality: string;
  pct: number;
  kind: DownloadKind;
  step: string;
  stats: string;
  canPause: boolean;
}

export type ImportStepState = 'done' | 'working' | 'pending';

export interface ImportStep {
  label: string;
  state: ImportStepState;
  detail: string;
  /** A path or file name: set in a monospace face. */
  mono: boolean;
}

export interface DownloadApp {
  name: string;
  status: string;
  connected: boolean;
}

export type HistoryOutcome = 'imported' | 'failed' | 'upgraded' | 'removed';

export interface HistoryRow {
  id: string;
  when: string;
  title: string;
  outcome: HistoryOutcome;
  what: string;
  source: string;
  size: string;
}

export interface DownloadsView {
  counts: { downloading: number; importing: number; queued: number };
  /** The connected apps, in words, e.g. "qBittorrent and SABnzbd". */
  connectedApps: string;
  now: DownloadItem[];
  importing: { title: string; quality: string; server: string; steps: ImportStep[] };
  apps: DownloadApp[];
  history: HistoryRow[];
}

/* ------------------------------------------------------------------ Settings */

export type RowAction = 'test' | 'edit' | 'add' | 'setup' | 'revoke';

export interface ConnectionRow {
  name: string;
  /** A small tag beside the name, e.g. "Torznab". */
  tag?: string;
  status: string;
  /** Shown as a dot with the status text. */
  state: 'on' | 'off';
  /** The button at the right of the row; none when the row has nothing to do. */
  action?: RowAction;
}

export type McpAbility = 'see' | 'ask' | 'downloads' | 'settings' | 'delete' | 'approve';

export interface McpKey {
  name: string;
  status: string;
  abilities: Array<{ id: McpAbility; allowed: boolean }>;
}

export interface SettingsView {
  downloadApps: ConnectionRow[];
  mediaServers: ConnectionRow[];
  sources: ConnectionRow[];
  keys: McpKey[];
  alerts: ConnectionRow[];
}
