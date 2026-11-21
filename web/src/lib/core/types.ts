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
  status: Status;
  caption: string;
  tone: CaptionTone;
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

export interface FeaturedTitle {
  id: string;
  kind: TitleKind;
  title: string;
  year: number;
  /** Quality of the copy in the library, e.g. "1080p". */
  quality: string;
  /** Media server that plays it, e.g. "Jellyfin". */
  server: string;
  /** Quality offered by the second button, e.g. "4K". */
  askQuality: string;
  meta: string;
  blurb: string;
  theme: HeroTheme;
}

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
