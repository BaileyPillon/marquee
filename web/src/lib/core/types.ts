export type TitleKind = 'movie' | 'tv' | 'artist';

/** What a poster's badge shows. Downloading carries its percentage. */
export type Status =
  | { kind: 'library' }
  | { kind: 'requested' }
  | { kind: 'missing' }
  | { kind: 'downloading'; pct: number };

/** Caption emphasis under a poster: quiet (muted), asked (soft white), waiting (accent). */
export type CaptionTone = 'quiet' | 'asked' | 'waiting';

export interface PosterItem {
  id: string;
  kind: TitleKind;
  title: string;
  year: string;
  /** Left out: no badge (the library has no file information for this person). */
  status?: Status;
  caption: string;
  tone: CaptionTone;
  /** Where the card leads. Defaults to the page of the title with this id. */
  href?: string;
  /** The real artwork (an artwork ref), over the typographic poster. */
  imageRef?: string;
}

/** Colours of a featured title's panel and its art. */
export interface HeroTheme {
  /** Section background. */
  bg: string;
  /** Background of the art panel. */
  artBg: string;
  /** The moon disc. */
  disc: string;
  /** Lighter and darker tower tones. */
  towerA: string;
  towerB: string;
}

/** What a title panel needs at minimum; the skyline art also reads the other three colours. */
export type PanelTheme = Pick<HeroTheme, 'bg' | 'artBg'> &
  Partial<Pick<HeroTheme, 'disc' | 'towerA' | 'towerB'>>;

export interface PosterRowData {
  id: string;
  title: string;
  /** Where "See all" goes. */
  href: string;
  items: PosterItem[];
}

export interface ComingEvent {
  day: string;
  date: string;
  title: string;
  status: string;
  /** 0 to 100 when a download is under way. */
  progress?: number;
  href: string;
}
