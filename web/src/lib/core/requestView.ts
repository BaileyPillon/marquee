// How a request reads in words: who asked, what for, where it stands. Pure functions over what
// `GET /requests` sends, used by the Requests page, the phone card, Home and the title pages.
import { formatBytes, formatDayLabel, kindLabel } from './format';
import { formatDate, formatList, formatNumber, lowerCase, t } from './i18n';
import { libraryIndex } from './libraryIndex.svelte';
import { peopleState } from './people.svelte';
import { uiKind } from './posterItems';
import type { RequestItem } from './requestsApi';
import { session } from './session.svelte';
import type { CaptionTone, PosterItem } from './types';

export interface Fact {
  label: string;
  value: string;
}

/** Just the name: "Priya". */
export function askerFirst(item: RequestItem): string {
  return item.user?.name ?? t('requests.someone');
}

/** Who asked, as a sentence part: "Priya", or "You" for the reader's own asks. */
export function askerName(item: RequestItem): string {
  if (item.user && item.user.id === session.me?.id) return t('requests.you');
  return askerFirst(item);
}

function resolution(item: RequestItem): string {
  if (item.title.kind === 'artist') return '';
  return item.quality === 'uhd' ? t('requests.res.uhd') : t('requests.res.hd');
}

/** "4K (2160p) · separate copy", "All episodes · HD (1080p)", "Seasons 1, 2 · HD (1080p)". */
export function askWhat(item: RequestItem): string {
  const parts: string[] = [];
  const scope = item.scope ?? {};
  if (item.title.kind === 'show') {
    const seasons = scope.seasons ?? [];
    if (seasons.length === 1) parts.push(t('requests.scope.season', { n: seasons[0] ?? 0 }));
    else if (seasons.length > 1) {
      parts.push(t('requests.scope.seasons', { list: formatList(seasons.map((n) => formatNumber(n))) }));
    }
    else parts.push(t('requests.scope.allEpisodes'));
  } else if (item.title.kind === 'artist') {
    const albums = scope.albums ?? [];
    parts.push(albums.length > 0 ? t('requests.scope.albums', { n: albums.length }) : t('requests.scope.allAlbums'));
  }
  const res = resolution(item);
  if (res) parts.push(res);
  if (item.quality === 'uhd' && item.title.kind !== 'artist') parts.push(t('requests.separateCopy'));
  return parts.join(' · ');
}

/** The short line under a title in the phone's "Also waiting" list: "Priya · 4K". */
export function briefLine(item: RequestItem): string {
  const who = askerFirst(item);
  if (item.quality === 'uhd') return t('requests.brief.4k', { who });
  if (item.title.kind === 'show') return t('requests.brief.all', { who });
  return who;
}

/** "Priya · today 08:30" for the card header. */
export function whoWhen(item: RequestItem): string {
  const when = lowerCase(formatDayLabel(item.createdAt));
  const time = new Date(item.createdAt);
  const clock = Number.isNaN(time.getTime()) ? '' : formatDate(time, { hour: '2-digit', minute: '2-digit' });
  return t('requests.whoWhen', { who: askerFirst(item), when, time: clock });
}

/** "Why it's waiting", from the person's rules when they are known. */
function whyWaiting(item: RequestItem): string {
  const person = item.user ? peopleState.byId.get(item.user.id) : undefined;
  const who = askerFirst(item);
  if (item.quality === 'uhd') {
    return person && person.autoApprove.uhd ? t('requests.why.overLimit', { who }) : t('requests.why.uhd');
  }
  if (person && !person.autoApprove.hd) return t('requests.why.autoOff', { who });
  if (person && person.autoApprove.hd) return t('requests.why.overLimit', { who });
  return t('requests.why.default');
}

/** The usage line "1 of 2", or "1 (no limit)". */
function usageValue(used: number, limit: number | null): string {
  return limit === null ? t('requests.usage.noLimit', { used }) : t('requests.usage.of', { used, limit });
}

function libraryValue(item: RequestItem): string {
  const files = libraryIndex.titles.get(item.title.id);
  if (!files) return t('requests.nothingYet');
  return files.quality || t('requests.inLibrary');
}

/** The rows on a waiting card (desktop) and on the big phone card. */
export function askFacts(item: RequestItem): { facts: Fact[]; phoneFacts: Fact[] } {
  const person = item.user ? peopleState.byId.get(item.user.id) : undefined;
  const who = askerFirst(item);
  const q = item.quality === 'uhd' ? t('ask.quality.uhd.short') : t('ask.quality.hd.short');
  const size =
    item.facts.sizeEstimate && item.facts.sizeEstimate > 0
      ? t('requests.aboutSize', { size: formatBytes(item.facts.sizeEstimate) })
      : t('requests.sizeUnknown');
  const folder = item.facts.folderPath || t('requests.folderDefault');
  const usage = person ? (item.quality === 'uhd' ? person.usage.uhd : person.usage.hd) : undefined;
  const usageRow: Fact[] = usage
    ? [{ label: t('requests.fact.asksWeek', { who, quality: q }), value: usageValue(usage.used, usage.limit) }]
    : [];

  const facts: Fact[] = [
    { label: t('requests.fact.size'), value: size },
    { label: t('requests.fact.folder'), value: folder },
    ...usageRow,
    ...(libraryIndex.status === 'ready'
      ? [{ label: t('requests.fact.inLibrary'), value: libraryValue(item) }]
      : []),
    { label: t('requests.fact.why'), value: whyWaiting(item) },
  ];
  const phoneFacts: Fact[] = [
    { label: t('requests.fact.quality'), value: askWhat(item) || q },
    { label: t('requests.fact.size'), value: size },
    { label: t('requests.fact.folder'), value: folder },
    ...usageRow,
  ];
  if (item.note) {
    const note = { label: t('requests.fact.note'), value: item.note };
    facts.push(note);
    phoneFacts.push(note);
  }
  return { facts, phoneFacts };
}

/** "1927 · Movie": year and kind on the big phone card. */
export function posterMeta(item: RequestItem): string {
  return [item.title.year ? String(item.title.year) : '', kindLabel(item.title.kind)]
    .filter(Boolean)
    .join(' · ');
}

/** The small poster's year line: "1939 · TV". */
export function posterYearLine(item: RequestItem): string {
  const y = item.title.year ? String(item.title.year) : '';
  if (item.title.kind === 'show') return y ? t('poster.yearTv', { year: y }) : t('poster.tv');
  if (item.title.kind === 'artist') return t('poster.artist');
  return y;
}

/** The line under a request: "Priya · approved by you", "Marco's Plex watchlist · approved automatically". */
export function decidedLine(item: RequestItem): string {
  const me = session.me?.id;
  const asker = askerName(item);
  const lead =
    item.source === 'watchlist'
      ? t('requests.line.watchlist', { who: askerFirst(item) })
      : item.source === 'mcp'
        ? t('requests.line.claude', { who: asker })
        : item.user?.id === me
          ? t('requests.line.youAsked')
          : asker;
  const q = item.quality === 'uhd' ? ` (${t('ask.quality.uhd.short')})` : '';
  const leadQ = lead + q;
  const by = item.decidedBy ? (item.decidedBy.id === me ? t('requests.by.you') : item.decidedBy.name) : '';
  switch (item.state) {
    case 'pending':
      return t('requests.line.waiting', { lead: leadQ });
    case 'declined': {
      const reason = item.declineReason ? ` — ‘${item.declineReason}’` : '';
      return t('requests.line.declined', { lead: leadQ, by: by || t('requests.by.owner') }) + reason;
    }
    case 'cancelled':
      return t('requests.line.cancelled', { lead: leadQ });
    case 'failed':
      return t('requests.line.failed', { lead: leadQ });
    default:
      return by
        ? t('requests.line.approvedBy', { lead: leadQ, by })
        : t('requests.line.approvedAuto', { lead: leadQ });
  }
}

/** When it was decided, for the right-hand column. */
export function decidedWhen(item: RequestItem): string {
  return formatDayLabel(item.decidedAt ?? item.updatedAt);
}

/**
 * Poster cards for "Your friends want": what is waiting first, then what was approved lately. One
 * card per title, so a title asked in HD and 4K shows once.
 */
export function askPosterItems(items: RequestItem[], staff: boolean): PosterItem[] {
  const seen = new Set<number>();
  const out: PosterItem[] = [];
  const order = [
    ...items.filter((i) => i.state === 'pending'),
    ...items.filter((i) => i.state === 'approved' || i.state === 'partial' || i.state === 'available'),
  ];
  for (const it of order) {
    if (seen.has(it.title.id)) continue;
    seen.add(it.title.id);
    const who = it.user?.id === session.me?.id ? t('requests.you') : askerFirst(it);
    const lead = it.quality === 'uhd' ? `${who} · ${t('ask.quality.uhd.short')}` : who;
    let caption: string;
    let tone: CaptionTone = 'asked';
    if (it.state === 'pending') {
      tone = staff ? 'waiting' : 'asked';
      caption =
        it.source === 'watchlist'
          ? t('home.ask.fromWatchlistWaiting', { who: askerFirst(it) })
          : staff
            ? t('home.ask.waitingForYou', { lead })
            : t('home.ask.waitingMember');
    } else if (it.source === 'watchlist') {
      caption = t('home.ask.fromWatchlist', { who: askerFirst(it) });
    } else if (it.state === 'available') {
      caption = t('home.ask.available', { lead });
    } else {
      caption = t('home.ask.approved', { lead });
    }
    out.push({
      id: String(it.title.id),
      kind: uiKind(it.title.kind),
      title: it.title.name,
      year: posterYearLine(it),
      status: it.state === 'available' ? { kind: 'library' } : { kind: 'requested' },
      caption,
      tone,
      ...(it.title.posterRef ? { imageRef: it.title.posterRef } : {}),
    });
    if (out.length >= 12) break;
  }
  return out;
}
