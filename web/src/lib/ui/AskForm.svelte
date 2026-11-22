<script lang="ts">
  import { onMount } from 'svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LabelValue, { type LabelValueItem } from './LabelValue.svelte';
  import Skeleton from './Skeleton.svelte';
  import { ApiError } from '../core/api';
  import {
    createRequest,
    failureKey,
    isRouteMissing,
    type ApiKind,
    type AskQuality,
    type AskTarget,
  } from '../core/catalog';
  import { formatDay } from '../core/format';
  import { t } from '../core/i18n';
  import { requestsState } from '../core/requests.svelte.ts';

  interface Props {
    target: AskTarget;
    kind: ApiKind;
    defaultQuality: AskQuality;
    /** The library already has it in HD: only 4K is offered. */
    haveHd: boolean;
    /** The ask is made. Says whether it was approved on the spot. */
    onsent: (autoApproved: boolean) => void;
    oncancel: () => void;
  }

  let { target, kind, defaultQuality, haveHd, onsent, oncancel }: Props = $props();

  const uid = $props.id();
  const noteId = `ask-note-${uid}`;

  // A fresh look at this week's allowance every time the sheet opens.
  let loadFailed = $state(false);
  onMount(() => {
    void requestsState.refresh().then((outcome) => {
      loadFailed = outcome === 'failed' && requestsState.availability === 'unknown';
    });
  });

  const summary = $derived(requestsState.summary);
  const off = $derived(requestsState.availability === 'unavailable');
  const ready = $derived(requestsState.availability === 'ready');

  // Music has no quality to choose; it goes in as the standard copy.
  const choosesQuality = $derived(kind !== 'artist');
  let picked = $state<AskQuality | undefined>();
  const quality = $derived<AskQuality>(
    !choosesQuality ? 'hd' : (picked ?? (haveHd ? 'uhd' : defaultQuality)),
  );
  let note = $state('');
  let busy = $state(false);
  let error = $state('');
  let missing = $state(false);
  let group: HTMLElement | undefined = $state();
  let noteInput: HTMLInputElement | undefined = $state();

  const usage = $derived(quality === 'uhd' ? summary?.usage.uhd : summary?.usage.hd);
  const facts = $derived.by<LabelValueItem[]>(() => {
    if (!summary) return [];
    const rows: LabelValueItem[] = [];
    const hd = summary.usage.hd;
    const uhd = summary.usage.uhd;
    if (hd.limit !== null && hd.limit > 0) {
      rows.push({
        label: t('ask.usage.hd'),
        value: t('ask.usage.value', { used: hd.used, limit: hd.limit }),
      });
    }
    if (choosesQuality && uhd.limit !== null && uhd.limit > 0) {
      rows.push({
        label: t('ask.usage.uhd'),
        value: t('ask.usage.value', { used: uhd.used, limit: uhd.limit }),
      });
    }
    return rows;
  });
  const atLimit = $derived(!!usage && usage.limit !== null && usage.used >= usage.limit);

  // Once the form is in, put focus on what the person will touch first.
  $effect(() => {
    if (!ready || missing) return;
    if (group) group.querySelector<HTMLElement>('[aria-pressed="true"]')?.focus();
    else noteInput?.focus();
  });

  function choose(id: AskQuality, unavailable: boolean): void {
    if (!unavailable) picked = id;
  }

  function limitMessage(err: ApiError): string {
    const limit = Number(err.details['limit']);
    const used = Number(err.details['used']);
    const resets = typeof err.details['resetsAt'] === 'string' ? err.details['resetsAt'] : '';
    const which = quality === 'uhd' ? t('ask.quality.uhd.short') : t('ask.quality.hd.short');
    if (Number.isFinite(limit) && limit > 0) {
      const when = formatDay(resets);
      return when
        ? t('ask.error.limitWhen', { used: Number.isFinite(used) ? used : limit, limit, which, when })
        : t('ask.error.limit', { used: Number.isFinite(used) ? used : limit, limit, which });
    }
    return t('ask.error.limitPlain', { which });
  }

  async function send(): Promise<void> {
    if (busy) return;
    busy = true;
    error = '';
    try {
      const answer = await createRequest(target, quality, note);
      void requestsState.refresh();
      onsent(answer.autoApproved === true);
    } catch (err) {
      if (isRouteMissing(err)) missing = true;
      else if (err instanceof ApiError && err.code === 'limit_reached') error = limitMessage(err);
      else if (err instanceof ApiError && err.code === 'already_requested') {
        error = t('ask.error.already');
      } else error = t(failureKey(err));
    } finally {
      busy = false;
    }
  }
</script>

{#if off || missing}
  <!-- The requests API is not built yet: say so quietly, never a crash. -->
  <div class="off" role="status">
    <p class="off-title">{t('ask.off.title')}</p>
    <p class="off-body">{t('ask.off.body')}</p>
  </div>
{:else if ready}
  {#if choosesQuality}
    <div class="choices" role="group" aria-label={t('ask.quality')} bind:this={group}>
      <button
        type="button"
        class="choice"
        class:pressed={quality === 'hd'}
        class:unavailable={haveHd}
        aria-pressed={quality === 'hd'}
        aria-disabled={haveHd}
        onclick={() => choose('hd', haveHd)}
      >
        <span class="ring" aria-hidden="true"></span>
        <span class="words">
          <span class="name">{haveHd ? t('ask.quality.hd.have') : t('ask.quality.hd')}</span>
          <span class="sub">
            {#if haveHd}<Icon name="check" size={14} stroke={2.4} />{/if}
            <span>{haveHd ? t('ask.quality.hd.haveSub') : t('ask.quality.hd.sub')}</span>
          </span>
        </span>
      </button>
      <button
        type="button"
        class="choice"
        class:pressed={quality === 'uhd'}
        aria-pressed={quality === 'uhd'}
        onclick={() => choose('uhd', false)}
      >
        <span class="ring" aria-hidden="true"></span>
        <span class="words">
          <span class="name">{t('ask.quality.uhd')}</span>
          <span class="sub"><span>{t('ask.quality.uhd.sub')}</span></span>
        </span>
      </button>
    </div>
  {/if}

  {#if facts.length > 0}<LabelValue items={facts} gap={14} />{/if}

  <div class="note">
    <label for={noteId}>{t('ask.noteLabel')}</label>
    <input
      id={noteId}
      type="text"
      bind:value={note}
      bind:this={noteInput}
      placeholder={t('ask.notePlaceholder')}
      autocomplete="off"
    />
  </div>

  {#if error}
    <p class="failed" role="alert">{error}</p>
  {:else if atLimit}
    <p class="failed" role="status">{t('ask.error.limitPlain', { which: quality === 'uhd' ? t('ask.quality.uhd.short') : t('ask.quality.hd.short') })}</p>
  {/if}

  <div class="send">
    <Button variant="primary" size="xl" block disabled={busy} onclick={send}>
      <Icon name="send" size={20} stroke={2.2} />
      {busy ? t('ask.sending') : t('ask.send')}
    </Button>
  </div>
{:else}
  <div class="loading" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    <Skeleton height="62px" radius="var(--radius-lg)" />
    <Skeleton height="62px" radius="var(--radius-lg)" />
    <Skeleton height="96px" radius="var(--radius-md)" />
  </div>
  {#if loadFailed}
    <p class="failed" role="alert">{t('load.failed')}</p>
  {/if}
{/if}

<Button variant="ghost" size="lg" block onclick={oncancel}>
  {off || missing ? t('ask.close') : t('ask.cancel')}
</Button>

<style>
  .off {
    margin: 14px 0 12px;
    padding: 14px 16px;
    border: 1px dashed var(--line-dashed);
    border-radius: var(--radius-lg);
  }
  .off-title {
    font-size: 15px;
    font-weight: 600;
    line-height: 22px;
    color: var(--text);
  }
  .off-body {
    margin-top: 2px;
    font-size: 13.5px;
    line-height: 20px;
    color: var(--text-3);
  }
  .choices {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 14px;
  }
  .choice {
    display: flex;
    align-items: center;
    gap: 12px;
    box-sizing: border-box;
    width: 100%;
    min-height: 62px;
    padding: 8px 14px;
    border: 2px solid var(--input-border);
    border-radius: var(--radius-lg);
    background: transparent;
    color: var(--text);
    text-align: start;
    cursor: pointer;
  }
  .choice.pressed {
    border-color: var(--accent);
    background: var(--input);
  }
  .choice.unavailable {
    border-style: dashed;
    color: var(--text-3);
    cursor: not-allowed;
  }
  .ring {
    flex: 0 0 auto;
    box-sizing: border-box;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    border: 2px solid var(--step-ring);
    background: transparent;
  }
  .pressed .ring {
    border-color: var(--accent);
    background: var(--accent);
    box-shadow: inset 0 0 0 3px var(--input);
  }
  .words {
    flex: 1 1 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .name {
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text-3);
  }
  .pressed .name {
    font-size: 16px;
    font-weight: 700;
    line-height: 22px;
    color: var(--text);
  }
  .sub {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
  }
  .pressed .sub {
    color: var(--text-2);
  }

  .note {
    margin-top: 14px;
  }
  label {
    display: block;
    margin: 0 0 6px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  input {
    box-sizing: border-box;
    width: 100%;
    height: 48px;
    padding: 0 14px;
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    background: var(--input);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 15px;
    line-height: 1;
    color-scheme: dark;
  }
  input::placeholder {
    color: var(--muted);
    opacity: 1;
  }
  .send {
    margin: 16px 0 4px;
  }

  .loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin: 14px 0 16px;
  }
  .failed {
    margin: 12px 0 0;
    font-size: 14px;
    line-height: 20px;
    color: var(--danger);
  }
</style>
