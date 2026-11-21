// i18n seam. English only for now: every user-visible string goes through t().
//
// Later (M10): load a translated catalog from the server and call setMessages(). Components
// that need to react to a language switch can then key on a locale store; today there is none.
import en from './i18n/en.json';

export type MessageKey = keyof typeof en;
export type MessageParams = Record<string, string | number>;

const fallback: Record<string, string> = en;
let active: Record<string, string> = fallback;

/** Replace the active catalog. Missing keys fall back to English. */
export function setMessages(messages: Record<string, string>): void {
  active = { ...fallback, ...messages };
}

/** Look up a message and fill `{name}` placeholders from params. */
export function t(key: MessageKey, params?: MessageParams): string {
  const raw = active[key] ?? key;
  if (!params) return raw;
  return raw.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in params ? String(params[name]) : whole,
  );
}
