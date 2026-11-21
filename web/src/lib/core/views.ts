// A few small shapes shared by pages: how an episode stands, the steps of an import, and a row of
// the Title info panel. What the APIs send lives in catalog.ts, requestsApi.ts, downloadsApi.ts,
// sourcesApi.ts and serversApi.ts.
//
// Strings in these types are content (names, dates, sizes, reasons the server wrote). Interface
// text lives in en.json and goes through t().

/** How an episode stands: unknown when the library's file list is not available to this person. */
export type EpisodeState = 'library' | 'downloading' | 'missing' | 'upcoming' | 'unknown';

/** One step of the import strip on Downloads. */
export type ImportStepState = 'done' | 'working' | 'pending';

export type RowAction = 'test' | 'edit' | 'add' | 'setup' | 'revoke';

/** A row of the Title info panel (TMDB, TVmaze, MusicBrainz); see providerRows.ts. */
export interface ConnectionRow {
  name: string;
  /** A small tag beside the name, e.g. "Movies". */
  tag?: string;
  status: string;
  /** Shown as a dot with the status text. */
  state: 'on' | 'off';
  /** The button at the right of the row; none when the row has nothing to do. */
  action?: RowAction;
}

/** A button at the right of a row on Settings (Test, Edit, Revoke). */
export interface RowButton {
  id: string;
  label: string;
  /** Names the button for assistive tech, e.g. "Test qBittorrent". */
  aria: string;
  onclick: () => void;
  busy?: boolean;
  busyLabel?: string;
}

/** One row of a panel on Settings › Connections. */
export interface PanelRow {
  key: string | number;
  name: string;
  /** A small tag beside the name, e.g. "Torznab". */
  tag?: string;
  status: string;
  /** Shown as a dot with the status text. */
  state: 'on' | 'off';
  /** A line under the status (an address, say), set in a monospace face. */
  detail?: string;
  /** A plain line under the status (what an alert channel tells you about). */
  note?: string;
  buttons?: RowButton[];
  /** What the last Test said, shown under the row. */
  result?: { ok: boolean; text: string };
}
