<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import AuthLayout from '../lib/accounts/AuthLayout.svelte';
  import ErrorNote from '../lib/accounts/ErrorNote.svelte';
  import Field from '../lib/accounts/Field.svelte';
  import SubmitButton from '../lib/accounts/SubmitButton.svelte';
  import { authErrorKey, errorCode, formatCountdown, inviteToken } from '../lib/accounts/errors';
  import { ApiError, postJSON } from '../lib/core/api';
  import { t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { current, navigate } from '../lib/core/router';
  import { landingPath, session, type User } from '../lib/core/session.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';

  const DEFAULT_WAIT_S = 60;

  let name = $state('');
  let password = $state('');
  let submitted = $state(false);
  let busy = $state(false);
  /** Wrong name or password: shown under the password field. */
  let credsError = $state('');
  /** Anything else that went wrong (server down, too many tries): shown above the button. */
  let formError = $state('');

  // Too many tries: the server says how long to wait (Retry-After); count it down.
  let waitUntil = $state(0);
  let remaining = $state(0);

  let nameField: Field | undefined = $state();
  let passwordField: Field | undefined = $state();

  const phone = $derived(!$isDesktop);
  const waiting = $derived(remaining > 0);

  const nameError = $derived(submitted && !name.trim() ? t('auth.required.name') : '');
  const passwordError = $derived(
    credsError || (submitted && password === '' ? t('auth.required.password') : ''),
  );

  onMount(() => {
    // A desktop visitor can start typing straight away; on a phone that would raise the keyboard.
    if (untrack(() => $isDesktop)) nameField?.focus();
  });

  // Someone already signed in who lands here (the back button, a stale bookmark) has no use for it.
  $effect(() => {
    if (session.me) navigate(landingPath($current.query.get('next')), { replace: true });
  });

  // Tick the countdown down from a fixed deadline, so a throttled timer cannot make it drift.
  $effect(() => {
    if (!waitUntil) return;
    const update = (): void => {
      const left = Math.ceil((waitUntil - Date.now()) / 1000);
      if (left > 0) {
        remaining = left;
        return;
      }
      remaining = 0;
      waitUntil = 0;
      formError = '';
    };
    update();
    const timer = window.setInterval(update, 500);
    return () => window.clearInterval(timer);
  });

  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (busy || waiting) return;
    submitted = true;
    credsError = '';
    formError = '';
    if (!name.trim()) return nameField?.focus();
    if (password === '') return passwordField?.focus();

    busy = true;
    try {
      const res = await postJSON<{ user: User }>('auth/login', { name: name.trim(), password });
      session.adopt(res.user);
      navigate(landingPath($current.query.get('next')), { replace: true });
    } catch (err) {
      const code = errorCode(err);
      if (code === 'invalid_credentials') {
        credsError = t(authErrorKey(err));
        await tick();
        passwordField?.focus();
      } else if (code === 'too_many_attempts') {
        const seconds = err instanceof ApiError ? (err.retryAfter ?? DEFAULT_WAIT_S) : DEFAULT_WAIT_S;
        formError = t(authErrorKey(err));
        waitUntil = Date.now() + Math.max(seconds, 1) * 1000;
      } else {
        formError = t(authErrorKey(err));
      }
    } finally {
      busy = false;
    }
  }

  // "I have an invite": a small field to paste the link (or just its code) into.
  const uid = $props.id();
  const inviteId = `invite-${uid}`;
  let inviteOpen = $state(false);
  let inviteText = $state('');
  let inviteError = $state('');
  let inviteField: Field | undefined = $state();

  async function toggleInvite(): Promise<void> {
    inviteOpen = !inviteOpen;
    if (!inviteOpen) return;
    await tick();
    inviteField?.focus();
  }

  function goToInvite(event: SubmitEvent): void {
    event.preventDefault();
    const token = inviteToken(inviteText);
    if (!token) {
      inviteError = t('signin.inviteEmpty');
      inviteField?.focus();
      return;
    }
    navigate(`/invite/${encodeURIComponent(token)}`);
  }
</script>

<AuthLayout variant="signin" title={t('signin.title')}>
  <form class="form" class:phone novalidate onsubmit={submit}>
    <Field
      bind:this={nameField}
      bind:value={name}
      label={t('signin.name')}
      name="name"
      autocomplete="username"
      error={nameError}
    />
    <Field
      bind:this={passwordField}
      bind:value={password}
      type="password"
      label={t('auth.password')}
      name="password"
      autocomplete="current-password"
      error={passwordError}
      oninput={() => (credsError = '')}
    />
    {#if formError}<ErrorNote>{formError}</ErrorNote>{/if}
    <SubmitButton size={phone ? 'lg' : 'md'} {busy} disabled={waiting}>
      {#if waiting}
        <span class="tabular">{t('signin.wait', { time: formatCountdown(remaining) })}</span>
      {:else}
        {busy ? t('signin.submitting') : t('signin.submit')}
        <Icon name="arrow" size={phone ? 20 : 18} stroke={2.2} />
      {/if}
    </SubmitButton>
  </form>

  {#snippet footer()}
    <p class="prompt">{phone ? t('signin.invitePromptPhone') : t('signin.invitePrompt')}</p>
    <button
      type="button"
      class="link"
      class:open={inviteOpen}
      aria-expanded={inviteOpen}
      aria-controls={inviteId}
      onclick={toggleInvite}
    >
      {t('signin.inviteButton')}
      <Icon name="arrow" size={16} stroke={2.2} />
    </button>
    <div id={inviteId} hidden={!inviteOpen}>
      {#if inviteOpen}
        <form class="invite" novalidate onsubmit={goToInvite}>
          <Field
            bind:this={inviteField}
            bind:value={inviteText}
            label={t('signin.inviteLabel')}
            name="invite"
            autocomplete="off"
            help={t('signin.inviteHelp')}
            error={inviteError}
            oninput={() => (inviteError = '')}
          />
          <Button type="submit" variant="outline" size="lg">{t('signin.inviteGo')}</Button>
        </form>
      {/if}
    </div>
  {/snippet}
</AuthLayout>

<style>
  .form {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }
  .form.phone {
    gap: 12px;
  }
  .tabular {
    font-variant-numeric: tabular-nums;
  }

  .prompt {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-2);
  }
  .link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    box-sizing: border-box;
    min-height: 48px;
    margin-top: 2px;
    padding: 0 4px 0 0;
    border: 0;
    background: transparent;
    color: var(--accent);
    font-family: var(--font-sans);
    font-size: 15px;
    font-weight: 600;
    line-height: 1;
    cursor: pointer;
  }
  .link:hover {
    color: var(--accent-hover);
  }
  .link :global(svg) {
    transition: transform 0.12s ease;
  }
  .link.open :global(svg) {
    transform: rotate(90deg);
  }
  .invite {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
    padding: 4px 0 8px;
  }
  .invite :global(.field) {
    align-self: stretch;
  }
</style>
