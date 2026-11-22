// Small formatters for what the catalog and library APIs send: sizes, times, dates, run times.
// Numbers, dates and relative times come from Intl in the language in use (see i18n.ts); anything
// that carries words goes through t().
import { activeLocale, formatDate, formatNumber, formatRelative, t } from './i18n';
import type { ApiKind, Title } from './catalog';

const BYTE_UNITS = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte', 'petabyte'] as const;

/** Binary steps, as disks report them: 1 KB is 1024 bytes. */
function scaleBytes(bytes: number): { value: number; unit: (typeof BYTE_UNITS)[number]; digits: number } {
  let value = bytes;
  let step = 0;
  while (value >= 1024 && step < BYTE_UNITS.length - 1) {
    value /= 1024;
    step += 1;
  }
  return { value, unit: BYTE_UNITS[step] ?? 'byte', digits: step === 0 || value >= 100 ? 0 : 1 };
}

/** 1_181_116_006 -> "1.1 GB" ("1,1 GB" in German). Binary units, as disks report them. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '';
  const { value, unit, digits } = scaleBytes(bytes);
  return formatNumber(value, {
    style: 'unit',
    unit,
    unitDisplay: 'short',
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
}

/** 153 -> "2 h 33 min"; 45 -> "45 min". Empty when unknown. */
export function formatRuntime(minutes: number): string {
  if (!minutes || minutes < 1) return '';
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (h === 0) return t('time.minutes', { m });
  return m === 0 ? t('time.hours', { h }) : t('time.hoursMinutes', { h, m });
}

/** 274000 -> "4:34". Empty when unknown. */
export function formatTrackLength(ms: number): string {
  if (!ms || ms < 1) return '';
  const total = Math.round(ms / 1000);
  return `${formatNumber(Math.floor(total / 60))}:${formatNumber(total % 60, { minimumIntegerDigits: 2 })}`;
}

function parseDay(iso: string): Date | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso);
  if (!m) return undefined;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
}

function startOfToday(): Date {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate());
}

/** "2010-10-22" -> "Fri 22 Oct 2010" (the year is left out for this year). Empty when unknown. */
export function formatAirDate(iso: string): string {
  const day = parseDay(iso);
  if (!day) return '';
  const sameYear = day.getFullYear() === new Date().getFullYear();
  return formatDate(day, {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    ...(sameYear ? {} : { year: 'numeric' }),
  });
}

/** Has this day not come yet? A missing date counts as not upcoming. */
export function isFuture(iso: string): boolean {
  const day = parseDay(iso);
  return !!day && day.getTime() > startOfToday().getTime();
}

/** "3 Nov", or "3 Nov 2025" for another year. */
export function formatDay(iso: string): string {
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return '';
  const sameYear = when.getFullYear() === new Date().getFullYear();
  return formatDate(when, {
    day: 'numeric',
    month: 'short',
    ...(sameYear ? {} : { year: 'numeric' }),
  });
}

/** "Added today", "Added yesterday", "Added 3 Nov". */
export function formatAdded(iso: string): string {
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return '';
  const today = startOfToday();
  const day = new Date(when.getFullYear(), when.getMonth(), when.getDate());
  const days = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  if (days <= 0) return t('added.today');
  if (days === 1) return t('added.yesterday');
  return t('added.on', { date: formatDay(iso) });
}

/** "just now", "5 min. ago", "2 hr. ago", "3 Nov": when something last worked. Empty when never. */
export function formatAgo(iso: string | null | undefined): string {
  if (!iso) return '';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '';
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (seconds < 90) return t('ago.moment');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return formatRelative(-minutes, 'minute');
  const hours = Math.round(minutes / 60);
  if (hours < 36) return formatRelative(-hours, 'hour');
  return formatDay(iso);
}

/** The year line on a poster: "2010", "2010 · TV", or just "Artist". */
export function posterYear(title: Pick<Title, 'kind' | 'year'>): string {
  if (title.kind === 'artist') return t('poster.artist');
  if (!title.year) return title.kind === 'show' ? t('poster.tv') : '';
  return title.kind === 'show' ? t('poster.yearTv', { year: String(title.year) }) : String(title.year);
}

/** What a kind is called in a sentence. */
export function kindLabel(kind: ApiKind): string {
  if (kind === 'show') return t('kind.show');
  if (kind === 'artist') return t('kind.artist');
  return t('kind.movie');
}

/** A short, stable plain-words count: "1 season", "3 seasons". */
export function seasonCount(n: number): string {
  return t('show.seasonCount', { n });
}

/** "ended" -> "Ended": the first letter in capitals (providers send statuses in lower case). */
export function sentence(text: string): string {
  return text ? text.charAt(0).toLocaleUpperCase(activeLocale()) + text.slice(1) : text;
}

/** "12:05" in the reader's own clock. */
function formatTime(when: Date): string {
  return formatDate(when, { hour: '2-digit', minute: '2-digit' });
}

/**
 * When something happened, short: "Today, 10:48", "Yesterday, 21:05", "Wed, 19:30" for the last
 * week, then "3 Nov". Empty when the date cannot be read.
 */
export function formatWhen(iso: string | null | undefined): string {
  if (!iso) return '';
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return '';
  const today = startOfToday();
  const day = new Date(when.getFullYear(), when.getMonth(), when.getDate());
  const days = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  const time = formatTime(when);
  if (days === 0) return t('when.today', { time });
  if (days === 1) return t('when.yesterday', { time });
  if (days > 1 && days < 7) {
    const weekday = formatDate(when, { weekday: 'short' });
    return t('when.weekday', { weekday, time });
  }
  return formatDay(iso);
}

/** "today", "yesterday", "5 min. ago" for the phone card and request lines. */
export function formatSince(iso: string | null | undefined): string {
  if (!iso) return '';
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return '';
  const today = startOfToday();
  const day = new Date(when.getFullYear(), when.getMonth(), when.getDate());
  const days = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  if (days <= 0) return formatAgo(iso);
  if (days === 1) return t('since.yesterday');
  return formatDay(iso);
}

/** Bytes per second: "14.2 MB/s". */
export function formatSpeed(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond < 0) return '';
  const { value, unit, digits } = scaleBytes(bytesPerSecond);
  return formatNumber(value, {
    style: 'unit',
    unit: `${unit}-per-second`,
    unitDisplay: 'short',
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
}

/** Seconds left: "6 min left", "1 h 20 min left". Empty when unknown. */
export function formatEta(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || seconds < 0) return '';
  if (seconds < 60) return t('eta.moment');
  const minutes = Math.round(seconds / 60);
  return t('eta.left', { time: formatRuntime(minutes) });
}

/** A copy's quality in words: "hd" -> "HD", "uhd" -> "4K", anything else as the server wrote it. */
export function qualityLabel(quality: string): string {
  if (quality === 'hd') return t('ask.quality.hd.short');
  if (quality === 'uhd') return t('ask.quality.uhd.short');
  return quality;
}

/** "2024-11-03" or a full timestamp to a Date on that local day. */
export function dayOf(iso: string): Date | undefined {
  return parseDay(iso);
}

/** "2026-11-21" for a local Date. This is a machine date for the API, never shown as text. */
export function isoDay(date: Date): string {
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${date.getFullYear()}-${m}-${d}`;
}

/** "Today", "Yesterday", else "19 Nov": the day something happened. */
export function formatDayLabel(iso: string | null | undefined): string {
  if (!iso) return '';
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return '';
  const today = startOfToday();
  const day = new Date(when.getFullYear(), when.getMonth(), when.getDate());
  const days = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  if (days <= 0) return t('requests.today');
  if (days === 1) return t('since.yesterdayCap');
  return formatDay(iso);
}
