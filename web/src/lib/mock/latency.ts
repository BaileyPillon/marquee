// The mock modules answer after a short pause, as the API will, so that pages show their loading
// state and handle a late answer. Set it to 0 to see the pages without the pause.
const MOCK_LATENCY_MS = 140;

/** Resolve with `value` after the mock latency. */
export function respond<T>(value: T): Promise<T> {
  if (MOCK_LATENCY_MS <= 0) return Promise.resolve(value);
  return new Promise((resolve) => {
    setTimeout(() => resolve(value), MOCK_LATENCY_MS);
  });
}
