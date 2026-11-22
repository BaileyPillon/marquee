<script lang="ts">
  import { onMount } from 'svelte';
  import Button from '../lib/ui/Button.svelte';
  import Chip from '../lib/ui/Chip.svelte';
  import Icon from '../lib/ui/Icon.svelte';
  import LoadFailed from '../lib/ui/LoadFailed.svelte';
  import NotSetUp from '../lib/ui/NotSetUp.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import Panel from '../lib/ui/Panel.svelte';
  import ProgressBar from '../lib/ui/ProgressBar.svelte';
  import Skeleton from '../lib/ui/Skeleton.svelte';
  import Tabs, { type TabItem } from '../lib/ui/Tabs.svelte';
  import {
    calendarDetail,
    calendarHref,
    calendarCompactName,
    calendarName,
    calendarProgress,
    calendarStateText,
  } from '../lib/core/calendarView';
  import { isRouteMissing } from '../lib/core/catalog';
  import { debounce, onEvent, onResync } from '../lib/core/events';
  import { formatDay, isoDay } from '../lib/core/format';
  import { activeLocale, formatDate, t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { getCalendar, type CalendarItem } from '../lib/core/serversApi';

  type View = 'month' | 'week';
  let view = $state<View>('month');
  /** Any day inside the range that is showing. */
  let anchor = $state(new Date());

  const viewTabs = $derived<TabItem[]>([
    { id: 'month', label: t('calendar.view.month') },
    { id: 'week', label: t('calendar.view.week') },
  ]);

  // ---- dates ----
  /** 1 (Monday) to 7 (Sunday): the first day of the week where the reader lives. */
  const firstDay: number = (() => {
    try {
      const locale = new Intl.Locale(activeLocale()) as unknown as {
        getWeekInfo?: () => { firstDay: number };
        weekInfo?: { firstDay: number };
      };
      const info = typeof locale.getWeekInfo === 'function' ? locale.getWeekInfo() : locale.weekInfo;
      return info?.firstDay ?? 1;
    } catch {
      return 1;
    }
  })();

  const addDays = (d: Date, n: number): Date => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
  const sameDay = (a: Date, b: Date): boolean => isoDay(a) === isoDay(b);

  function startOfWeek(d: Date): Date {
    const dow = d.getDay() === 0 ? 7 : d.getDay();
    return addDays(d, -((dow - firstDay + 7) % 7));
  }

  /** The days the table shows: whole weeks. */
  const days = $derived.by<Date[]>(() => {
    if (view === 'week') {
      const start = startOfWeek(anchor);
      return Array.from({ length: 7 }, (_, i) => addDays(start, i));
    }
    const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
    const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
    const start = startOfWeek(first);
    const end = addDays(startOfWeek(last), 6);
    const count = Math.round((end.getTime() - start.getTime()) / 86_400_000) + 1;
    return Array.from({ length: count }, (_, i) => addDays(start, i));
  });
  const weeks = $derived(
    Array.from({ length: Math.ceil(days.length / 7) }, (_, i) => days.slice(i * 7, i * 7 + 7)),
  );
  const from = $derived(isoDay(days[0] ?? anchor));
  const to = $derived(isoDay(days[days.length - 1] ?? anchor));

  const today = new Date();
  const rangeLabel = $derived(
    view === 'month'
      ? formatDate(anchor, { month: 'long', year: 'numeric' })
      : t('calendar.weekOf', {
          from: formatDay(days[0] ? isoDay(days[0]) + 'T12:00:00' : ''),
          to: formatDay(days[6] ? isoDay(days[6]) + 'T12:00:00' : ''),
        }),
  );

  const weekdayNames = $derived(
    (weeks[0] ?? []).map((d) => ({
      long: formatDate(d, { weekday: 'long' }),
      short: formatDate(d, { weekday: 'short' }),
    })),
  );

  function step(direction: -1 | 1): void {
    anchor =
      view === 'month'
        ? new Date(anchor.getFullYear(), anchor.getMonth() + direction, 1)
        : addDays(anchor, 7 * direction);
  }

  function goToday(): void {
    anchor = new Date();
  }

  /** The week that holds a day, in week view (from a "+ more" link). */
  function openWeek(day: Date): void {
    anchor = day;
    view = 'week';
  }

  // ---- loading ----
  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let status = $state<Status>('loading');
  let items = $state.raw<CalendarItem[]>([]);
  let controller: AbortController | undefined;

  async function load(quiet = false): Promise<void> {
    controller?.abort();
    const mine = (controller = new AbortController());
    if (!quiet) status = 'loading';
    try {
      const res = await getCalendar({ from, to }, mine.signal);
      if (mine.signal.aborted) return;
      items = res;
      status = 'ready';
    } catch (err) {
      if (mine.signal.aborted) return;
      status = isRouteMissing(err) ? 'off' : quiet && status === 'ready' ? 'ready' : 'failed';
    }
  }

  // A different range means a different question.
  $effect(() => {
    void from;
    void to;
    void load();
    return () => controller?.abort();
  });

  onMount(() => {
    const soon = debounce(() => void load(true), 1500);
    const stops = [
      onEvent('calendar', () => soon.call()),
      onEvent('downloads', () => soon.call()),
      onResync(() => soon.call()),
    ];
    return () => {
      soon.cancel();
      for (const stop of stops) stop();
    };
  });

  const byDay = $derived.by(() => {
    const map = new Map<string, CalendarItem[]>();
    for (const item of items) {
      const list = map.get(item.date) ?? [];
      list.push(item);
      map.set(item.date, list);
    }
    for (const list of map.values()) list.sort((a, b) => calendarName(a).localeCompare(calendarName(b)));
    return map;
  });

  const itemsOf = (d: Date): CalendarItem[] => byDay.get(isoDay(d)) ?? [];
  const MONTH_CELL_MAX = 3;

  // The phone reads as an agenda: the days that have something (every day in week view).
  const agenda = $derived(
    days.filter((d) => view === 'week' || itemsOf(d).length > 0 || sameDay(d, today)),
  );
  const inMonth = (d: Date): boolean => d.getMonth() === anchor.getMonth();
  const total = $derived(items.length);

  function dayLong(d: Date): string {
    return formatDate(d, { weekday: 'long', day: 'numeric', month: 'long' });
  }

  const uid = $props.id();
  const tableId = `cal-${uid}`;
</script>

{#snippet badge(item: CalendarItem)}
  {@const progress = calendarProgress(item)}
  {#if item.state === 'available'}
    <Chip tone="solid" size="micro">
      {#snippet icon()}<Icon name="check" size={11} stroke={2.8} />{/snippet}
      {calendarStateText(item)}
    </Chip>
  {:else if item.state === 'downloading'}
    <Chip tone="solid" size="micro">
      {#snippet icon()}<Icon name="downloads" size={11} stroke={2.6} />{/snippet}
      {calendarStateText(item)}
    </Chip>
  {:else if item.state === 'missing'}
    <Chip tone="inverse" size="micro">{calendarStateText(item)}</Chip>
  {:else}
    <Chip tone="dashed" size="micro">{calendarStateText(item)}</Chip>
  {/if}
  {#if progress !== undefined}
    <span class="bar"><ProgressBar value={progress} height={3} rounded /></span>
  {/if}
{/snippet}

{#snippet entry(item: CalendarItem, compact: boolean)}
  <a class="entry" class:compact href={calendarHref(item)}>
    <span class="entry-name" title={calendarName(item)}>{compact ? calendarCompactName(item) : calendarName(item)}</span>
    {#if !compact && calendarDetail(item)}<span class="entry-detail">{calendarDetail(item)}</span>{/if}
    <span class="entry-state">{@render badge(item)}</span>
  </a>
{/snippet}

<PageHeader title={t('route.calendar')} subtitle={t('calendar.subtitle')} scale="large" />

<div class="controls">
  <div class="nav" role="group" aria-label={t('calendar.navigate')}>
    <Button size="sm" icon aria-label={view === 'month' ? t('calendar.prevMonth') : t('calendar.prevWeek')} onclick={() => step(-1)}>
      <Icon name="back" size={18} stroke={2.2} />
    </Button>
    <Button size="sm" onclick={goToday}>{t('calendar.today')}</Button>
    <Button size="sm" icon aria-label={view === 'month' ? t('calendar.nextMonth') : t('calendar.nextWeek')} onclick={() => step(1)}>
      <Icon name="chevron" size={18} stroke={2.2} />
    </Button>
  </div>
  <h2 class="range" aria-live="polite">{rangeLabel}</h2>
  <Tabs
    kind="nav"
    items={viewTabs}
    value={view}
    onchange={(id) => (view = id === 'week' ? 'week' : 'month')}
    label={t('calendar.view')}
    prefix="calendar-view"
  />
</div>

{#if status === 'off'}
  <Panel tag="div" padding="18px">
    <NotSetUp hint={t('calendar.offHint')} />
  </Panel>
{:else if status === 'failed'}
  <LoadFailed onretry={() => void load()} />
{:else if status === 'loading' && items.length === 0}
  <div class="loading" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    <Skeleton height="320px" radius="var(--radius-lg)" />
  </div>
{:else}
  <p class="count" role="status">
    {total === 0 ? t('calendar.none') : t('calendar.count', { n: total })}
  </p>

  {#if $isDesktop}
    <Panel tag="div" padding="0" clip>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="scroll" role="region" aria-label={t("calendar.region", { range: rangeLabel })} tabindex="0">
        <table id={tableId} class:week={view === 'week'}>
          <caption class="sr-only">{t('calendar.caption', { range: rangeLabel })}</caption>
          <thead>
            <tr>
              {#each weekdayNames as name (name.long)}
                <th scope="col" abbr={name.long}>{name.short}</th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each weeks as week (isoDay(week[0] ?? today))}
              <tr>
                {#each week as day (isoDay(day))}
                  {@const list = itemsOf(day)}
                  {@const shown = view === 'month' ? list.slice(0, MONTH_CELL_MAX) : list}
                  <td class:other={view === 'month' && !inMonth(day)} class:today={sameDay(day, today)}>
                    <time class="date" datetime={isoDay(day)} aria-current={sameDay(day, today) ? 'date' : undefined}>
                      <span aria-hidden="true">{day.getDate()}</span>
                      <span class="sr-only">{dayLong(day)}</span>
                    </time>
                    {#if shown.length > 0}
                      <ul>
                        {#each shown as item (item.titleId + ':' + item.date + ':' + (item.season ?? '') + ':' + (item.episode ?? ''))}
                          <li>{@render entry(item, view === 'month')}</li>
                        {/each}
                      </ul>
                    {/if}
                    {#if list.length > shown.length}
                      <button type="button" class="more" onclick={() => openWeek(day)}>
                        {t('calendar.more', { n: list.length - shown.length })}
                        <span class="sr-only">{dayLong(day)}</span>
                      </button>
                    {/if}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </Panel>
  {:else}
    <!-- A phone reads this as a list of days: a seven-column grid would not fit. -->
    {#if agenda.length === 0}
      <p class="empty">{t('calendar.none')}</p>
    {:else}
      <ol class="agenda" aria-label={t('calendar.caption', { range: rangeLabel })}>
        {#each agenda as day (isoDay(day))}
          {@const list = itemsOf(day)}
          <li>
            <h3 class:today={sameDay(day, today)}>
              <time datetime={isoDay(day)} aria-current={sameDay(day, today) ? 'date' : undefined}>{dayLong(day)}</time>
            </h3>
            {#if list.length > 0}
              <ul>
                {#each list as item (item.titleId + ':' + item.date + ':' + (item.season ?? '') + ':' + (item.episode ?? ''))}
                  <li>
                    <Panel tag="div" radius="md" padding="12px 14px">{@render entry(item, false)}</Panel>
                  </li>
                {/each}
              </ul>
            {:else}
              <p class="quiet">{t('calendar.nothingDay')}</p>
            {/if}
          </li>
        {/each}
      </ol>
    {/if}
  {/if}
{/if}

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px 16px;
    margin-bottom: 16px;
  }
  .nav {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .range {
    flex: 1 1 auto;
    min-width: 0;
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .count {
    margin-bottom: 12px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .loading {
    margin-top: 8px;
  }
  .empty,
  .quiet {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }

  .scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    min-width: 820px;
    border-collapse: collapse;
    table-layout: fixed;
  }
  th {
    padding-block: 10px;
    padding-inline: 12px 10px;
    border-bottom: 1px solid var(--hairline-strong);
    text-align: start;
    font-size: 11.5px;
    font-weight: 600;
    line-height: 16px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  td {
    vertical-align: top;
    height: 118px;
    padding-block: 8px 10px;
    padding-inline: 10px 8px;
    border-inline-start: 1px solid var(--hairline-strong);
    border-top: 1px solid var(--hairline-strong);
  }
  td:first-child {
    border-inline-start: 0;
  }
  tbody tr:first-child td {
    border-top: 0;
  }
  table.week td {
    height: 280px;
  }
  td.other {
    background: var(--rail);
  }
  td.other .date {
    color: var(--muted);
  }
  .date {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 26px;
    height: 26px;
    padding: 0 6px;
    margin-bottom: 4px;
    border-radius: var(--radius-pill);
    font-size: 13px;
    font-weight: 600;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    color: var(--text-2);
  }
  td.today .date {
    background: var(--accent);
    color: var(--on-accent);
  }
  td ul {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .more {
    margin-top: 6px;
    padding: 0;
    border: 0;
    background: transparent;
    font-size: 12.5px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .more:hover {
    color: var(--text);
  }

  .entry {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    background: var(--rail);
    box-shadow: inset 0 0 0 1px var(--hairline);
    color: var(--text);
    text-decoration: none;
  }
  .entry:hover {
    background: var(--panel-hover);
    color: var(--text);
  }
  .entry-name {
    overflow: hidden;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .entry-detail {
    font-size: 12.5px;
    line-height: 17px;
    color: var(--text-3);
  }
  .entry-state {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
  }
  .bar {
    flex: 1 1 40px;
    min-width: 40px;
  }
  /* In a card of its own (the phone's agenda) the name wraps instead of being cut. */
  .agenda .entry {
    padding: 0;
    background: transparent;
    box-shadow: none;
  }
  .agenda .entry-name {
    overflow: visible;
    font-size: 15px;
    line-height: 20px;
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .agenda {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }
  .agenda h3 {
    margin-bottom: 8px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .agenda h3.today {
    color: var(--accent);
  }
  .agenda ul {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
