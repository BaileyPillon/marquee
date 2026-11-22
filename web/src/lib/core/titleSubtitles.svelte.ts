// The subtitles of a movie's or a show's video files, kept up to date: it loads when the title is
// known, loads again when the server says a subtitle was saved or removed (an automatic download
// after an import, or someone else's change), and `reload()` asks again without blanking what is
// shown. Call it while a component is being set up (it uses an effect).
import { debounce, onEvent, onResync } from './events';
import { getTitleSubtitles, type TitleSubtitleFile } from './subtitlesApi';

export interface TitleSubtitles {
  readonly files: TitleSubtitleFile[];
  reload(): void;
}

export function watchTitleSubtitles(titleId: () => number | undefined): TitleSubtitles {
  let files = $state.raw<TitleSubtitleFile[]>([]);
  let attempt = $state(0);

  $effect(() => {
    const id = titleId();
    void attempt;
    if (id === undefined) {
      files = [];
      return;
    }
    let live = true;
    const controller = new AbortController();

    async function load(): Promise<void> {
      try {
        const list = await getTitleSubtitles(id as number, controller.signal);
        if (live) files = list;
      } catch {
        // Not there or not allowed: show nothing rather than a guess.
        if (live && !controller.signal.aborted) files = [];
      }
    }

    void load();
    const soon = debounce(() => void load(), 400);
    const stop = onEvent('subtitles', () => soon.call());
    const stopResync = onResync(() => soon.call());
    return () => {
      live = false;
      controller.abort();
      soon.cancel();
      stop();
      stopResync();
    };
  });

  return {
    get files() {
      return files;
    },
    reload() {
      attempt += 1;
    },
  };
}
