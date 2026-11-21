// Everyone's limits and this week's use, for the owner and admins (`GET /people`). Shared by the
// Requests page (a card says "Priya's 4K asks this week: 1 of 2") and the People & limits panel.
import { ApiError } from './api';
import { getPeople, type PeopleResult, type PersonInfo } from './requestsApi';

export type PeopleStatus = 'idle' | 'loading' | 'ready' | 'failed' | 'unavailable';

class PeopleState {
  status = $state<PeopleStatus>('idle');
  people = $state.raw<PersonInfo[]>([]);
  defaults = $state.raw<PeopleResult['defaults'] | undefined>(undefined);
  #run = 0;

  readonly byId = $derived(new Map(this.people.map((p) => [p.id, p])));

  /** Ask the server. Safe to call again: the last answer wins. */
  async reload(): Promise<void> {
    const run = ++this.#run;
    if (this.status === 'idle' || this.status === 'failed') this.status = 'loading';
    try {
      const res = await getPeople();
      if (run !== this.#run) return;
      this.people = res.people;
      this.defaults = res.defaults;
      this.status = 'ready';
    } catch (err) {
      if (run !== this.#run) return;
      const status = err instanceof ApiError ? err.status : 0;
      this.status = status === 403 || status === 404 || status === 405 ? 'unavailable' : 'failed';
    }
  }

  /** One person changed (a limits save answered with the fresh row). */
  replace(person: PersonInfo): void {
    this.people = this.people.map((p) => (p.id === person.id ? person : p));
  }

  clear(): void {
    this.#run += 1;
    this.status = 'idle';
    this.people = [];
    this.defaults = undefined;
  }
}

export const peopleState = new PeopleState();
