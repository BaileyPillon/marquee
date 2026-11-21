// Turning what the accounts API says into words (and into the field it belongs to).
import { ApiError } from '../core/api';
import type { MessageKey } from '../core/i18n';

const byCode: Record<string, MessageKey> = {
  invalid_name: 'auth.error.invalid_name',
  name_taken: 'auth.error.name_taken',
  weak_password: 'auth.error.weak_password',
  password_too_long: 'auth.error.password_too_long',
  owner_exists: 'auth.error.owner_exists',
  invalid_invite: 'auth.error.invalid_invite',
  invalid_credentials: 'auth.error.invalid_credentials',
  too_many_attempts: 'auth.error.too_many_attempts',
};

/** The friendly sentence for a failed accounts call. */
export function authErrorKey(err: unknown): MessageKey {
  if (!(err instanceof ApiError)) return 'auth.error.generic';
  if (err.status === 0) return 'auth.error.network';
  return byCode[err.code] ?? 'auth.error.generic';
}

export function errorCode(err: unknown): string {
  return err instanceof ApiError ? err.code : '';
}

export type FormField = 'name' | 'password' | 'confirm' | 'form';

/** Which field a server error is about; `form` means the whole form. */
export function errorField(code: string): FormField {
  switch (code) {
    case 'invalid_name':
    case 'name_taken':
      return 'name';
    case 'weak_password':
    case 'password_too_long':
      return 'password';
    default:
      return 'form';
  }
}

/** 299 seconds -> "4:59". */
export function formatCountdown(totalSeconds: number): string {
  const s = Math.max(0, Math.ceil(totalSeconds));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, '0')}`;
}

/** Pull the invite code out of whatever was pasted: a full link, a path, or the bare code. */
export function inviteToken(pasted: string): string {
  const text = pasted.trim();
  const inLink = /\/invite\/([^/?#\s]+)/.exec(text);
  if (inLink?.[1]) {
    try {
      return decodeURIComponent(inLink[1]);
    } catch {
      return inLink[1];
    }
  }
  return text;
}
