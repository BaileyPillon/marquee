<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import AuthLayout from '../lib/accounts/AuthLayout.svelte';
  import ErrorNote from '../lib/accounts/ErrorNote.svelte';
  import Field from '../lib/accounts/Field.svelte';
  import SubmitButton from '../lib/accounts/SubmitButton.svelte';
  import { authErrorKey, errorCode, errorField, type FormField } from '../lib/accounts/errors';
  import { health, postJSON } from '../lib/core/api';
  import { t, type MessageKey } from '../lib/core/i18n';
  import { isDesktop } from '../lib/core/media';
  import { navigate } from '../lib/core/router';
  import { session, type User } from '../lib/core/session.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Icon from '../lib/ui/Icon.svelte';

  const MIN_PASSWORD = 8;
  const STEPS: MessageKey[] = ['setup.step.account', 'setup.step.library', 'setup.step.apps'];

  let name = $state('');
  let password = $state('');
  let confirm = $state('');
  let touched = $state({ name: false, password: false, confirm: false });
  let submitted = $state(false);
  let busy = $state(false);
  /** What the server refused, and which field it was about. Cleared when that field is edited. */
  let refusal = $state<{ field: FormField; code: string; message: string } | null>(null);
  /** Set once step 1 is done: the owner exists and is signed in. */
  let created = $state<User | null>(null);

  let layout: AuthLayout | undefined = $state();
  let nameField: Field | undefined = $state();
  let passwordField: Field | undefined = $state();
  let confirmField: Field | undefined = $state();
  let version = $state('');

  const port = location.port || (location.protocol === 'https:' ? '443' : '80');

  // Someone opened /setup on an install that already has its owner: send them to sign-in.
  onMount(() => {
    untrack(() => {
      if (session.status === 'ready' && !session.needsOwner && !created) {
        navigate(session.me ? '/' : '/signin', { replace: true });
      }
    });
    health()
      .then((h) => {
        version = h.version ?? '';
      })
      .catch(() => {});
    // Start typing straight away on a desktop. On a phone that would raise the keyboard and
    // scroll past the scene, and when the scene stacks above the form the field is off screen.
    if (untrack(() => $isDesktop)) nameField?.focus({ preventScroll: true });
  });

  const step = $derived(created ? 1 : 0);

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
    // Land on the first mistake.
    if (!name.trim()) return nameField?.focus();
    if (password.length < MIN_PASSWORD) return passwordField?.focus();
    if (confirm !== password) return confirmField?.focus();

    busy = true;
    try {
      const res = await postJSON<{ user: User }>('setup/owner', {
        name: name.trim(),
        password,
      });
      session.adopt(res.user);
      password = '';
      confirm = '';
      created = res.user;
      await tick();
      layout?.focusHeading();
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

  // The file name sits in a chip in the middle of the sentence; the translation holds {file}.
  const MARK = '';
  const [keyBefore = '', keyAfter = ''] = t('setup.keyNote', { file: MARK }).split(MARK);
</script>

<AuthLayout
  bind:this={layout}
  variant="setup"
  title={created ? t('setup.doneTitle', { name: created.name }) : t('setup.title')}
  intro={created ? t('setup.doneIntro') : t('setup.intro')}
>
  <p class="step-of">{t('setup.stepOf', { n: step + 1, total: STEPS.length, name: t(STEPS[step] as MessageKey) })}</p>
  <ol class="steps" aria-label={t('setup.progress')}>
    {#each STEPS as label, i (label)}
      {@const done = i < step}
      {@const now = i === step}
      <li aria-current={now ? 'step' : undefined}>
        <div class="bar" class:lit={done || now} aria-hidden="true"></div>
        <div class="row">
          <span class="dot" class:now class:done aria-hidden="true">
            {#if done}<Icon name="check" size={12} stroke={3} />{:else}{i + 1}{/if}
          </span>
          <span class="label" class:now>
            {t(label)}
            {#if done}
              <span class="sr-only">({t('setup.step.done')})</span>
            {:else if !now}
              <span class="sr-only">({t('setup.step.upcoming')})</span>
            {/if}
          </span>
        </div>
      </li>
    {/each}
  </ol>

  {#if created}
    <div class="next">
      <Button href="/settings" variant="primary" size="lg" block>
        {t('setup.continue')}
        <Icon name="arrow" size={18} stroke={2.2} />
      </Button>
      <Button href="/" variant="outline" size="lg" block>{t('setup.skip')}</Button>
    </div>
  {:else}
    <form class="form" novalidate onsubmit={submit}>
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
      {#if refusal?.field === 'form'}
        <div class="refusal">
          <ErrorNote>{refusal.message}</ErrorNote>
          {#if refusal.code === 'owner_exists'}
            <a class="inline-link" href="/signin">{t('setup.signInInstead')}</a>
          {/if}
        </div>
      {/if}
      <SubmitButton {busy}>
        {busy ? t('setup.creating') : t('setup.create')}
        <Icon name="arrow" size={18} stroke={2.2} />
      </SubmitButton>
    </form>
  {/if}

  <div class="keynote">
    <Icon name="lock" size={18} />
    <p>{keyBefore}<span class="chip">marquee.key</span>{keyAfter}</p>
  </div>

  <p class="version">
    {version ? t('setup.version', { version, port }) : t('setup.port', { port })}
  </p>
</AuthLayout>

<style>
  .step-of {
    margin: 0 0 10px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }

  .steps {
    display: flex;
    gap: 10px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .steps li {
    flex: 1 1 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .bar {
    height: 4px;
    border-radius: 2px;
    background: var(--step-bar);
  }
  .bar.lit {
    background: var(--accent);
  }
  .row {
    display: flex;
    align-items: flex-start;
    gap: 7px;
  }
  .dot {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    border: 1.5px solid var(--step-ring);
    background: transparent;
    color: var(--muted);
    font-size: 11px;
    font-weight: 700;
    line-height: 1;
  }
  .dot.now {
    background: var(--accent);
    border: 1px solid var(--accent);
    color: var(--on-accent);
  }
  .dot.done {
    border: 1.5px solid var(--accent);
    color: var(--accent);
  }
  .label {
    min-width: 0;
    font-size: 13px;
    font-weight: 500;
    line-height: 20px;
    color: var(--muted);
  }
  .label.now {
    font-weight: 700;
    color: var(--text);
  }

  .form {
    display: flex;
    flex-direction: column;
    gap: 18px;
    margin-top: 28px;
  }
  .refusal {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .inline-link {
    align-self: flex-start;
    margin-inline-start: 24px;
    font-size: 13px;
    font-weight: 600;
  }

  .next {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 28px;
  }

  .keynote {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    margin-top: 22px;
    padding-top: 20px;
    border-top: 1px solid var(--hairline);
    color: var(--muted);
  }
  .keynote :global(svg) {
    margin-top: 1px;
  }
  .keynote p {
    font-size: 13px;
    line-height: 19px;
    color: var(--muted);
  }
  .chip {
    box-sizing: border-box;
    padding: 1px 6px;
    border-radius: 4px;
    background: var(--input);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
    font-size: 12.5px;
    font-weight: 600;
    line-height: 17px;
    color: var(--text-2);
  }

  .version {
    margin-top: 28px;
    font-size: 12.5px;
    font-weight: 500;
    line-height: 18px;
    letter-spacing: 0.02em;
    color: var(--muted);
  }
</style>
