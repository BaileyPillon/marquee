// One shared server-sent-events stream for the whole app (`GET /api/v1/events`).
//
// Pages call `onEvent('downloads', fn)` and get a function that stops listening. The stream opens
// when the first listener arrives and closes when the last one leaves. The browser reconnects by
// itself; when it does, the `onResync` listeners run so a page can ask again for what it missed.
//
// Frames look like `event: <topic>` and `data: <json>`; the payloads are described next to the
// code that publishes them (downloads: {type, id, ...}; requests: {event, id, state}).

/** The topics the app listens to. A topic nobody publishes on is harmless. */
const TOPICS = ['requests', 'downloads', 'titles', 'watchlists', 'calendar', 'alerts'];
const STREAM_URL = `/api/v1/events?topic=${TOPICS.join(',')}`;

export type EventHandler = (data: unknown) => void;

const listeners = new Map<string, Set<EventHandler>>();
const resyncs = new Set<() => void>();
let source: EventSource | null = null;
let opened = false;
let lost = false;

function total(): number {
  let n = resyncs.size;
  for (const set of listeners.values()) n += set.size;
  return n;
}

function open(): void {
  if (source || typeof EventSource === 'undefined') return;
  // Same origin, so the session cookie goes with it.
  const es = new EventSource(STREAM_URL);
  source = es;
  es.onopen = () => {
    if (opened && lost) for (const fn of [...resyncs]) fn();
    opened = true;
    lost = false;
  };
  es.onerror = () => {
    lost = true;
  };
  for (const topic of TOPICS) {
    es.addEventListener(topic, (event) => {
      let data: unknown;
      try {
        data = JSON.parse((event as MessageEvent<string>).data);
      } catch {
        return; // A frame we cannot read is skipped.
      }
      for (const fn of [...(listeners.get(topic) ?? [])]) fn(data);
    });
  }
}

function closeIfIdle(): void {
  if (total() > 0 || !source) return;
  source.close();
  source = null;
  opened = false;
  lost = false;
}

/** Listen to one topic. Returns the function that stops listening. */
export function onEvent(topic: string, fn: EventHandler): () => void {
  let set = listeners.get(topic);
  if (!set) listeners.set(topic, (set = new Set()));
  set.add(fn);
  open();
  return () => {
    set.delete(fn);
    closeIfIdle();
  };
}

/** Run `fn` after the stream came back from a drop, so the page can reload what it missed. */
export function onResync(fn: () => void): () => void {
  resyncs.add(fn);
  open();
  return () => {
    resyncs.delete(fn);
    closeIfIdle();
  };
}

/** A function that runs `fn` at most once per `ms`, after the last call (for bursts of events). */
export function debounce(fn: () => void, ms: number): { call: () => void; cancel: () => void } {
  let timer: number | undefined;
  return {
    call() {
      window.clearTimeout(timer);
      timer = window.setTimeout(fn, ms);
    },
    cancel() {
      window.clearTimeout(timer);
    },
  };
}
