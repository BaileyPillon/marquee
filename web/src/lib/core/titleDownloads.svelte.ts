// What is coming in for one title right now: its active downloads, moving as the `downloads`
// event stream says so. Call it while a component is being set up (it uses an effect). The list
// is empty for a person who may not see downloads.
import { listDownloads, type DownloadEvent, type DownloadView } from './downloadsApi';
import { debounce, onEvent, onResync } from './events';

export interface TitleDownloads {
  readonly items: DownloadView[];
}

export function watchTitleDownloads(
  titleId: () => number | undefined,
  enabled: () => boolean,
): TitleDownloads {
  let items = $state.raw<DownloadView[]>([]);

  $effect(() => {
    const id = titleId();
    if (id === undefined || !enabled()) {
      items = [];
      return;
    }
    let live = true;

    async function load(): Promise<void> {
      try {
        const res = await listDownloads({ state: 'active', limit: 200 });
        if (live) items = res.downloads.filter((d) => d.title?.id === id);
      } catch {
        if (live) items = []; // not allowed or not there: nothing to show
      }
    }

    void load();
    const soon = debounce(() => void load(), 250);
    const stop = onEvent('downloads', (raw) => {
      const ev = raw as DownloadEvent;
      if (ev.type === 'progress' && ev.items) {
        const byId = new Map(ev.items.map((i) => [i.id, i]));
        if (!items.some((d) => byId.has(d.id))) return;
        items = items.map((d) => {
          const p = byId.get(d.id);
          return p
            ? { ...d, state: p.state, progress: p.progress, speedBps: p.speedBps, etaSeconds: p.etaSeconds }
            : d;
        });
        return;
      }
      soon.call();
    });
    const stopResync = onResync(() => soon.call());
    return () => {
      live = false;
      soon.cancel();
      stop();
      stopResync();
    };
  });

  return {
    get items() {
      return items;
    },
  };
}
