// The status line of an app, a media server or a source: from when it last answered and what went wrong.
import { formatAgo } from './format';
import { t } from './i18n';

export function connectionStatus(c: {
  enabled?: boolean;
  lastOkAt?: string | null;
  lastError?: string;
}): { state: 'on' | 'off'; text: string } {
  if (c.enabled === false) return { state: 'off', text: t('conn.off') };
  if (c.lastError) return { state: 'off', text: t('conn.notAnswering', { why: c.lastError }) };
  if (c.lastOkAt) return { state: 'on', text: t('conn.working', { ago: formatAgo(c.lastOkAt) }) };
  return { state: 'off', text: t('conn.untested') };
}
