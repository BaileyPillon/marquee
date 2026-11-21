<script lang="ts">
  import { t } from '../core/i18n';
  import type { ComingEvent } from '../core/types';

  interface Props {
    events: ComingEvent[];
    /** Said in place of the list when there is nothing to show ("Nothing in the next 7 days"). */
    note?: string;
  }

  let { events, note }: Props = $props();

  const uid = $props.id();
  const headingId = `coming-${uid}`;
</script>

<section aria-labelledby={headingId}>
  <div class="head">
    <h2 id={headingId}>
      {t('coming.title')}
    </h2>
    <a class="all" href="/calendar">{t('action.openCalendar')}</a>
  </div>
  {#if events.length === 0 && note}
    <p class="note">{note}</p>
  {/if}
  <ul class="scroller">
    {#each events as ev (ev.day + ev.date + ev.title)}
      <li>
        <a class="event" href={ev.href}>
          <div class="when">
            <div class="day">{ev.day}</div>
            <div class="date">{ev.date}</div>
          </div>
          <div class="what">
            <div class="name">{ev.title}</div>
            <div class="state">{ev.status}</div>
          </div>
          {#if ev.progress !== undefined}
            <div class="bar" aria-hidden="true"><div style:width="{ev.progress}%"></div></div>
          {/if}
        </a>
      </li>
    {/each}
  </ul>
</section>

<style>
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
  }
  h2 {
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .note {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .all {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
    text-decoration: none;
  }
  .all:hover {
    color: var(--text);
  }
  .scroller {
    display: flex;
    gap: 12px;
    overflow-x: auto;
    padding-bottom: 4px;
    scrollbar-width: thin;
    scrollbar-color: var(--input-border) transparent;
  }
  li {
    display: flex;
    flex: 1 0 240px;
  }
  .event {
    position: relative;
    box-sizing: border-box;
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    gap: 14px;
    min-width: 0;
    padding: 10px 14px;
    overflow: hidden;
    border-radius: var(--radius-md);
    background: var(--panel);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
    color: var(--text);
    text-decoration: none;
  }
  .event:hover {
    color: var(--text);
    background: var(--panel-hover);
  }
  .when {
    flex: 0 0 40px;
    text-align: center;
  }
  .day {
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .date {
    font-size: 20px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .what {
    flex: 1 1 auto;
    min-width: 0;
  }
  .name {
    overflow: hidden;
    font-size: 14px;
    font-weight: 600;
    line-height: 19px;
    color: var(--text);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .state {
    font-size: 12.5px;
    font-weight: 500;
    line-height: 17px;
    color: var(--text-3);
  }
  .bar {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 4px;
    background: var(--track-faint);
  }
  .bar div {
    height: 4px;
    background: var(--text);
  }

  @media (max-width: 899.98px) {
    .head {
      align-items: center;
      min-height: 44px;
      margin-bottom: 0;
    }
    .all {
      display: inline-flex;
      align-items: center;
      min-height: 44px;
      padding-left: 12px;
    }
    .scroller {
      margin: 0 -16px;
      padding: 0 16px 4px;
      scrollbar-width: none;
    }
  }
</style>
