// Loads the data of one page. Call it while a component is being set up (it uses an effect).
//
// `load` is any function that returns a promise: a mock from lib/mock today, a call through
// api.ts later. Late answers for an earlier key are dropped, and `reload()` asks again.

export interface Resource<T> {
  /** The loaded value, or undefined while loading or after a failure. */
  readonly data: T | undefined;
  readonly failed: boolean;
  reload(): void;
}

export function resource<T>(key: () => string, load: (key: string) => Promise<T>): Resource<T> {
  let data = $state.raw<T | undefined>(undefined);
  let failed = $state(false);
  let attempt = $state(0);

  $effect(() => {
    const current = key();
    void attempt;
    let live = true;
    data = undefined;
    failed = false;
    load(current).then(
      (value) => {
        if (live) data = value;
      },
      () => {
        if (live) failed = true;
      },
    );
    return () => {
      live = false;
    };
  });

  return {
    get data() {
      return data;
    },
    get failed() {
      return failed;
    },
    reload() {
      attempt += 1;
    },
  };
}
