// How a calendar item reads: its name, its episode code, its state in words, its day. Used by Home
// ("Coming up") and the Calendar page.
import { titlePath } from './catalog';
import { dayOf } from './format';
import { t } from './i18n';
import type { CalendarItem } from './serversApi';

/** "S01E04" for an episode, empty for anything else. */
export function episodeCode(item: CalendarItem): string {
  if (item.season === null || item.season === undefined) return '';
  if (item.episode === null || item.episode === undefined) return '';
  return `S${String(item.season).padStart(2, '0')}E${String(item.episode).padStart(2, '0')}`;
}

/** "Pioneer One S01E04", or just the name for a movie or an album. */
export function calendarName(item: CalendarItem): string {
  const code = episodeCode(item);
  return code ? `${item.name} ${code}` : item.name;
}

/** For a narrow cell: the code first, so it survives when the name is cut short ("S13E28 · Show"). */
export function calendarCompactName(item: CalendarItem): string {
  const code = episodeCode(item);
  return code ? `${code} · ${item.name}` : item.name;
}

/** The episode's own title, when there is one. */
export function calendarDetail(item: CalendarItem): string {
  return (item.episodeName ?? '').trim();
}

/** Where an item leads: the page of its show or movie. */
export function calendarHref(item: CalendarItem): string {
  return titlePath(item.kind === 'episode' ? 'show' : 'movie', item.titleId);
}

/** 0 to 100 while downloading. The server may send a fraction (0 to 1) or a percentage. */
export function calendarProgress(item: CalendarItem): number | undefined {
  if (item.state !== 'downloading' || item.progress === undefined || item.progress === null) return undefined;
  const p = item.progress <= 1 ? item.progress * 100 : item.progress;
  return Math.max(0, Math.min(100, Math.round(p)));
}

export function calendarStateText(item: CalendarItem): string {
  switch (item.state) {
    case 'available':
      return t('calendar.state.available');
    case 'downloading': {
      const p = calendarProgress(item);
      return p === undefined ? t('calendar.state.downloadingPlain') : t('status.downloading', { pct: p });
    }
    case 'missing':
      return t('calendar.state.missing');
    default:
      return t('calendar.state.expected');
  }
}

/** Day of the week and day of the month for the little date block: "Sat", "21". */
export function dayParts(iso: string): { day: string; date: string } {
  const when = dayOf(iso);
  if (!when) return { day: '', date: '' };
  return {
    day: new Intl.DateTimeFormat(undefined, { weekday: 'short' }).format(when),
    date: String(when.getDate()),
  };
}
