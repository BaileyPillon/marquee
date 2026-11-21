// Mock data for the app shell (the rail's owner block and the Requests badge).
import type { Section } from '../core/router';

export const owner = {
  name: 'Bailey',
  initial: 'B',
  role: 'owner' as const,
};

/** Counts shown on rail items: asks waiting for the owner's yes. */
export const badges: Partial<Record<Section, number>> = {
  requests: 2,
};
