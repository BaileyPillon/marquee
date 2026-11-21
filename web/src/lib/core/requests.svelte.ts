// The asks summary: what is waiting for the owner, and how much of this week's allowance a person
// has used. The requests API (M4) may not be built yet; until `GET /requests/summary` answers, this
// stays "unavailable" and nothing in the shell shows a number.
import { getRequestsSummary, isRouteMissing, type RequestsSummary } from './catalog';

export type RequestsAvailability = 'unknown' | 'ready' | 'unavailable';

const POLL_MS = 60_000;
const RECHECK_MS = 300_000;

class RequestsState {
  summary = $state.raw<RequestsSummary | undefined>(undefined);
  availability = $state<RequestsAvailability>('unknown');
  /** Asks waiting for this person's yes (owner and admin), 0 when none or not known. */
  readonly waiting = $derived(this.summary?.waitingForYou ?? 0);

  #run = 0;

  /**
   * Ask the server now. Never throws. Resolves "ok", "missing" (the route is not there yet) or
   * "failed" (no answer; what we last knew stays).
   */
  async refresh(): Promise<'ok' | 'missing' | 'failed'> {
    const run = ++this.#run;
    try {
      const summary = await getRequestsSummary();
      if (run === this.#run) {
        this.summary = summary;
        this.availability = 'ready';
      }
      return 'ok';
    } catch (err) {
      if (isRouteMissing(err)) {
        if (run === this.#run) {
          this.summary = undefined;
          this.availability = 'unavailable';
        }
        return 'missing';
      }
      return 'failed';
    }
  }

  /** Keep the summary fresh while the shell is up. Returns a function that stops it. */
  start(): () => void {
    let timer: number | undefined;
    let alive = true;
    const tick = async (): Promise<void> => {
      if (document.visibilityState !== 'hidden') await this.refresh();
      if (!alive) return;
      timer = window.setTimeout(tick, this.availability === 'unavailable' ? RECHECK_MS : POLL_MS);
    };
    void tick();
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }

  /** Forget everything (someone signed out). */
  clear(): void {
    this.#run += 1;
    this.summary = undefined;
    this.availability = 'unknown';
  }
}

export const requestsState = new RequestsState();
