// Artists that were saved before their albums arrived. Adding an artist can answer 202
// `{title, hydrating:true}`; the albums are fetched in the background and a `titles` event
// `{id, event:"hydrated"}` follows. The artist page shows "Loading albums…" while its id is here.
import { SvelteSet } from 'svelte/reactivity';

class Hydration {
  readonly #ids = new SvelteSet<number>();

  add(id: number): void {
    this.#ids.add(id);
  }

  has(id: number): boolean {
    return this.#ids.has(id);
  }

  done(id: number): void {
    this.#ids.delete(id);
  }
}

export const hydration = new Hydration();
