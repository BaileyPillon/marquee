// Shapes of what the pages load. The mock modules in lib/mock/ build these today; the Go API will
// send the same shapes later, so a page never knows which one answered.
//
// Strings in these types are content (names, dates, sizes, reasons the server wrote). Interface
// text lives in en.json and goes through t().
import type { PanelTheme, PosterItem, Status } from './types';

/** One label and value pair, as the server describes a request, a rule or a limit. */
export interface Fact {
  label: string;
  value: string;
}

/* ------------------------------------------------------------------ Ask sheet */

export interface AskChoice {
  id: 'hd' | '4k';
  title: string;
  sub: string;
  /** Not offered: the library already has it. */
  unavailable: boolean;
}

export interface AskView {
  /** Who says yes to asks. */
  ownerName: string;
  choices: AskChoice[];
  selected: AskChoice['id'];
  facts: Fact[];
}

/* ------------------------------------------------------------------ Movie page */

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

export interface TitleView {
  id: string;
  title: string;
  year: number;
  /** Text after the check mark, e.g. "In your library · 1080p". */
  status: string;
  meta: string;
  blurb: string;
  theme: PanelTheme;
  genres: string[];
  /** Media server that plays it. */
  server: string;
  /** Quality offered by "Ask for …". */
  askQuality: string;
  library: { quality: string; fileName: string; details: string };
  copy4k: { status: Status; headline: string; asked: string; rule: string };
  releases: { note: string; items: Release[] };
  cast: string[];
  crew: Array<{ name: string; role: string }>;
  history: Array<{ date: string; kind: HistoryKind; text: string }>;
  similar: PosterItem[];
}

/* ------------------------------------------------------------------ Show page */

export type EpisodeState = 'library' | 'downloading' | 'missing' | 'upcoming';

export interface Episode {
  num: number;
  air: string;
  state: EpisodeState;
  /** What the server says about this episode, e.g. "In library · 1080p · 1.1 GB". */
  status: string;
  /** 0 to 100 while it downloads. */
  progress?: number;
  action: 'open' | 'search';
}

export interface ShowView {
  id: string;
  title: string;
  year: number;
  status: string;
  meta: string;
  blurb: string;
  theme: PanelTheme;
  server: string;
  /** Follow the show for new episodes. */
  watching: boolean;
  season: { number: number; have: number; total: number; episodes: Episode[] };
  rules: Fact[];
  similar: PosterItem[];
}

/* ------------------------------------------------------------------ Artist page */

export interface Album {
  id: string;
  title: string;
  years: string;
  status: Status;
  tracks: number;
  caption: string;
  /** Cover colour, standing in for artwork. */
  color: string;
}

export interface Track {
  num: number;
  title: string;
  album: string;
  length: string;
  format: string;
}

export interface SimilarArtist {
  id: string;
  name: string;
  detail: string;
  color: string;
}

export interface ArtistView {
  id: string;
  name: string;
  /** How the artist is named in a sentence, e.g. "Chopin". */
  shortName: string;
  /** Years of life, for the Roman numerals set into the art. */
  born: number;
  died: number;
  status: string;
  meta: string;
  blurb: string;
  theme: PanelTheme;
  /** Small caps line in the art, e.g. "NOCTURNES · BALLADES". */
  artCaption: string;
  server: string;
  /** Who says yes to asks. */
  ownerName: string;
  albums: { note: string; items: Album[] };
  tracks: { items: Track[]; shown: number; total: number };
  similar: SimilarArtist[];
}

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
  action: RowAction;
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
  titleInfo: ConnectionRow[];
  keys: McpKey[];
  alerts: ConnectionRow[];
}
