<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LabelValue from './LabelValue.svelte';
  import Skeleton from './Skeleton.svelte';
  import { t } from '../core/i18n';
  import { resource } from '../core/resource.svelte.ts';
  import type { AskChoice } from '../core/views';
  import { getAskOptions } from '../mock/ask';

  interface Props {
    titleId: string;
    /** The ask is made. Gets the name of the person who says yes. */
    onsend: (owner: string) => void;
    oncancel: () => void;
  }

  let { titleId, onsend, oncancel }: Props = $props();

  const uid = $props.id();
  const noteId = `ask-note-${uid}`;

  const options = resource(() => titleId, getAskOptions);
  const view = $derived(options.data);

  let picked = $state<AskChoice['id'] | undefined>();
  const quality = $derived(picked ?? view?.selected);
  let note = $state('');
  let group: HTMLElement | undefined = $state();

  // Once the options are in, put focus on the quality that is selected.
  $effect(() => {
    if (view) group?.querySelector<HTMLElement>('[aria-pressed="true"]')?.focus();
  });

  function choose(choice: AskChoice): void {
    if (!choice.unavailable) picked = choice.id;
  }
</script>

{#if view}
  <div class="choices" role="group" aria-label={t('ask.quality')} bind:this={group}>
    {#each view.choices as choice (choice.id)}
      {@const pressed = choice.id === quality}
      <button
        type="button"
        class="choice"
        class:pressed
        class:unavailable={choice.unavailable}
        aria-pressed={pressed}
        aria-disabled={choice.unavailable}
        onclick={() => choose(choice)}
      >
        <span class="ring" aria-hidden="true"></span>
        <span class="words">
          <span class="name">{choice.title}</span>
          <span class="sub">
            {#if choice.unavailable}<Icon name="check" size={14} stroke={2.4} />{/if}
            <span>{choice.sub}</span>
          </span>
        </span>
      </button>
    {/each}
  </div>

  <LabelValue items={view.facts} gap={14} />

  <div class="note">
    <label for={noteId}>{t('ask.noteLabel', { owner: view.ownerName })}</label>
    <input
      id={noteId}
      type="text"
      bind:value={note}
      placeholder={t('ask.notePlaceholder', { owner: view.ownerName })}
      autocomplete="off"
    />
  </div>

  <div class="send">
    <Button variant="primary" size="xl" block onclick={() => onsend(view.ownerName)}>
      <Icon name="send" size={20} stroke={2.2} />
      {t('ask.send')}
    </Button>
  </div>
{:else}
  <div class="loading" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    <Skeleton height="62px" radius="var(--radius-lg)" />
    <Skeleton height="62px" radius="var(--radius-lg)" />
    <Skeleton height="96px" radius="var(--radius-md)" />
  </div>
  {#if options.failed}
    <p class="failed" role="alert">{t('load.failed')}</p>
  {/if}
{/if}

<Button variant="ghost" size="lg" block onclick={oncancel}>{t('ask.cancel')}</Button>

<style>
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
    text-align: left;
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
    margin: 0 0 12px;
    font-size: 14px;
    color: var(--danger);
  }
</style>
