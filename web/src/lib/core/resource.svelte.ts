// Loads the data of one page. Call it while a component is being set up (it uses an effect).
//
// `load` is any function that returns a promise: usually a call through api.ts. Late answers for an earlier key are dropped (and the call is aborted), and `reload()`
// asks again.

export interface Resource<T> {
  /** The loaded value, or undefined while loading or after a failure. */
  readonly data: T | undefined;
  readonly failed: boolean;
  /** What the load threw, so a page can tell "not found" from "could not reach the server". */
  readonly error: unknown;
  reload(): void;
  /** Replace the loaded value (a save answered with the fresh copy). */
  set(value: T): void;
}

export function resource<T>(
  key: () => string,
  load: (key: string, signal: AbortSignal) => Promise<T>,
): Resource<T> {
  let data = $state.raw<T | undefined>(undefined);
  let failed = $state(false);
  let error = $state.raw<unknown>(undefined);
  let attempt = $state(0);

  $effect(() => {
    const current = key();
    void attempt;
    let live = true;
    const controller = new AbortController();
    data = undefined;
    failed = false;
    error = undefined;
    load(current, controller.signal).then(
      (value) => {
        if (live) data = value;
      },
      (err: unknown) => {
        if (!live) return;
        failed = true;
        error = err;
      },
    );
    return () => {
      live = false;
      controller.abort();
    };
  });

  return {
    get data() {
      return data;
    },
    get failed() {
      return failed;
    },
    get error() {
      return error;
    },
    reload() {
      attempt += 1;
    },
    set(value: T) {
      data = value;
      failed = false;
      error = undefined;
    },
  };
}
