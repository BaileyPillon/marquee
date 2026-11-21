// One short message at a time, shown by <Toast> and gone after a few seconds. Call createToaster()
// while a component is being set up (it clears its timer when the component goes away).

export interface Toaster {
  /** The message on screen, or null. */
  readonly message: string | null;
  show(message: string): void;
  clear(): void;
}

export function createToaster(ms = 4500): Toaster {
  let message = $state<string | null>(null);
  let timer: number | undefined;

  function clear(): void {
    window.clearTimeout(timer);
    message = null;
  }

  function show(next: string): void {
    window.clearTimeout(timer);
    message = next;
    timer = window.setTimeout(clear, ms);
  }

  $effect(() => () => window.clearTimeout(timer));

  return {
    get message() {
      return message;
    },
    show,
    clear,
  };
}
