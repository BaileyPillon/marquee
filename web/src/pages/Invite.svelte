<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import AuthLayout from '../lib/accounts/AuthLayout.svelte';
  import ErrorNote from '../lib/accounts/ErrorNote.svelte';
  import Field from '../lib/accounts/Field.svelte';
  import SubmitButton from '../lib/accounts/SubmitButton.svelte';
  import { authErrorKey, errorCode, errorField, type FormField } from '../lib/accounts/errors';
  import { postJSON } from '../lib/core/api';
  import { t } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { navigate, type Match } from '../lib/core/router';
  import { session, type User } from '../lib/core/session.svelte';
  import Icon from '../lib/ui/Icon.svelte';

  interface Props {
    match: Match;
  }

  let { match }: Props = $props();

  const MIN_PASSWORD = 8;
  const token = $derived(match.params['token'] ?? '');

  let name = $state('');
  let password = $state('');
  let confirm = $state('');
  let touched = $state({ name: false, password: false, confirm: false });
  let submitted = $state(false);
  let busy = $state(false);
  let refusal = $state<{ field: FormField; code: string; message: string } | null>(null);

  let nameField: Field | undefined = $state();
  let passwordField: Field | undefined = $state();
  let confirmField: Field | undefined = $state();

  const phone = $derived(!$isDesktop);

  onMount(() => {
    if (untrack(() => $isDesktop)) nameField?.focus();
  });

  const nameError = $derived.by(() => {
    if (refusal?.field === 'name') return refusal.message;
    if (!(touched.name || submitted)) return '';
    return name.trim() ? '' : t('auth.required.name');
  });
  const passwordError = $derived.by(() => {
    if (refusal?.field === 'password') return refusal.message;
    if (!(touched.password || submitted)) return '';
    return password.length >= MIN_PASSWORD ? '' : t('auth.tooShort');
  });
  const confirmError = $derived.by(() => {
    if (!(touched.confirm || submitted)) return '';
    if (confirm === '') return t('auth.required.confirm');
    return confirm === password ? '' : t('auth.mismatch');
  });

  function clearRefusal(field: FormField): void {
    if (refusal && (refusal.field === field || refusal.field === 'form')) refusal = null;
  }

  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (busy) return;
    submitted = true;
    refusal = null;
    if (!name.trim()) return nameField?.focus();
    if (password.length < MIN_PASSWORD) return passwordField?.focus();
    if (confirm !== password) return confirmField?.focus();

    busy = true;
    try {
      const res = await postJSON<{ user: User }>('invites/accept', {
        token,
        name: name.trim(),
        password,
      });
      session.adopt(res.user);
      navigate('/', { replace: true });
    } catch (err) {
      const field = errorField(errorCode(err));
      refusal = { field, code: errorCode(err), message: t(authErrorKey(err)) };
      await tick();
      if (field === 'name') nameField?.focus();
      else if (field === 'password') passwordField?.focus();
    } finally {
      busy = false;
    }
  }
</script>

<AuthLayout variant="signin" title={t('invite.title')} intro={t('invite.intro')}>
  <form class="form" class:phone novalidate onsubmit={submit}>
    <Field
      bind:this={nameField}
      bind:value={name}
      label={t('auth.yourName')}
      name="name"
      autocomplete="username"
      required
      error={nameError}
      onblur={() => (touched.name = true)}
      oninput={() => clearRefusal('name')}
    />
    <Field
      bind:this={passwordField}
      bind:value={password}
      type="password"
      label={t('auth.password')}
      name="password"
      autocomplete="new-password"
      required
      help={t('auth.passwordHelp')}
      error={passwordError}
      onblur={() => (touched.password = true)}
      oninput={() => clearRefusal('password')}
    />
    <Field
      bind:this={confirmField}
      bind:value={confirm}
      type="password"
      label={t('auth.confirm')}
      name="confirm"
      autocomplete="new-password"
      required
      error={confirmError}
      onblur={() => (touched.confirm = true)}
      oninput={() => clearRefusal('confirm')}
    />
    {#if refusal?.field === 'form'}<ErrorNote>{refusal.message}</ErrorNote>{/if}
    <SubmitButton size={phone ? 'lg' : 'md'} {busy}>
      {busy ? t('invite.submitting') : t('invite.submit')}
      <Icon name="arrow" size={phone ? 20 : 18} stroke={2.2} />
    </SubmitButton>
  </form>

  {#snippet footer()}
    <p class="prompt">{t('invite.haveAccount')}</p>
    <a class="link" href="/signin">
      {t('invite.signIn')}
      <Icon name="arrow" size={16} stroke={2.2} />
    </a>
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
    padding-block: 0;
    padding-inline: 0 4px;
    color: var(--accent);
    font-size: 15px;
    font-weight: 600;
    line-height: 1;
    text-decoration: none;
  }
  .link:hover {
    color: var(--accent-hover);
  }
</style>
