<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import ErrorNote from '../../lib/accounts/ErrorNote.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { ApiError } from '../../lib/core/api';
  import {
    getMetadataStatus,
    getSettings,
    putSettings,
    type MetadataStatus,
  } from '../../lib/core/catalog';
  import { t } from '../../lib/core/i18n';
  import { providerRows } from '../../lib/core/providerRows';
  import ConnectionPanel from './ConnectionPanel.svelte';

  interface Props {
    /** Only the owner reads and saves the key. */
    isOwner: boolean;
  }

  let { isOwner }: Props = $props();

  const uid = $props.id();
  const headId = `tmdb-${uid}`;
  const inputId = `tmdb-key-${uid}`;
  const helpId = `tmdb-help-${uid}`;

  // ---- provider status ----
  let status = $state.raw<MetadataStatus | undefined>();
  let statusFailed = $state(false);
  let attempt = $state(0);

  async function loadStatus(signal?: AbortSignal): Promise<void> {
    try {
      status = await getMetadataStatus(signal);
      statusFailed = false;
    } catch {
      if (!signal?.aborted) statusFailed = true;
    }
  }

  $effect(() => {
    void attempt;
    const controller = new AbortController();
    void loadStatus(controller.signal);
    return () => controller.abort();
  });

  // ---- the key ----
  /** What the server shows for the stored key (masked), or "" when there is none. */
  let stored = $state('');
  let key = $state('');
  let loaded = $state(false);
  let loadFailed = $state(false);
  let show = $state(false);
  let saving = $state(false);
  let saved = $state('');
  let error = $state('');

  $effect(() => {
    if (!isOwner) return;
    const controller = new AbortController();
    getSettings('tmdb', controller.signal).then(
      (section) => {
        const value = typeof section.value['apiKey'] === 'string' ? section.value['apiKey'] : '';
        stored = value;
        key = value;
        loaded = true;
      },
      () => {
        if (!controller.signal.aborted) loadFailed = true;
      },
    );
    return () => controller.abort();
  });

  const dirty = $derived(key !== stored);

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (saving || !dirty) return;
    saving = true;
    error = '';
    saved = '';
    try {
      // The masked value sent back unchanged would keep the stored key; an empty one clears it.
      const section = await putSettings('tmdb', { apiKey: key.trim() });
      const value = typeof section.value['apiKey'] === 'string' ? section.value['apiKey'] : '';
      stored = value;
      key = value;
      show = false;
      saved = value ? t('info.saved') : t('info.cleared');
      void loadStatus();
    } catch (err) {
      error = err instanceof ApiError && err.status === 0 ? t('error.network') : t('info.saveFailed');
    } finally {
      saving = false;
    }
  }

  // "Edit" on the TMDB row takes the owner to the key field; for anyone else the row has no button.
  const rows = $derived(
    (status ? providerRows(status) : []).map((row) =>
      isOwner ? row : { ...row, action: undefined },
    ),
  );

  function editRow(): void {
    document.getElementById(inputId)?.focus();
  }
</script>

<div class="grid">
  {#if isOwner}
    <Panel labelledby={headId} padding="18px">
      <SectionHead title={t('info.tmdb.heading')} id={headId} size="md" align="center" gap={6} minHeight={36} />
      <p class="help">{t('info.tmdb.help')}</p>
      {#if loadFailed}
        <LoadFailed onretry={() => location.reload()} />
      {:else if !loaded}
        <div role="status" aria-busy="true">
          <span class="sr-only">{t('load.loading')}</span>
          <Skeleton height="48px" radius="var(--radius-sm)" />
        </div>
      {:else}
        <form onsubmit={save} novalidate>
          <label for={inputId}>{t('info.tmdb.label')}</label>
          <div class="line">
            <input
              id={inputId}
              name="tmdbKey"
              type={show ? 'text' : 'password'}
              bind:value={key}
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              aria-describedby={helpId}
              oninput={() => {
                saved = '';
                error = '';
              }}
            />
            <Button
              size="lg"
              aria-label={show ? t('info.hideAria') : t('info.showAria')}
              onclick={() => (show = !show)}
            >
              {show ? t('info.hide') : t('info.show')}
            </Button>
          </div>
          <p id={helpId} class="small">{t('info.tmdb.keyHelp')}</p>
          {#if error}<div class="err"><ErrorNote>{error}</ErrorNote></div>{/if}
          <div class="submit">
            <Button variant="primary" type="submit" disabled={saving || !dirty}>
              {saving ? t('info.saving') : t('info.save')}
            </Button>
            <span class="saved" aria-live="polite">{saved}</span>
          </div>
        </form>
      {/if}
    </Panel>
  {:else}
    <Panel tag="div" padding="18px">
      <p class="help">{t('info.ownerOnly')}</p>
    </Panel>
  {/if}

  {#if status}
    <ConnectionPanel
      title={t('settings.panel.info')}
      {rows}
      note={t('settings.note.info')}
      onrow={isOwner ? editRow : undefined}
    />
  {:else if statusFailed}
    <LoadFailed onretry={() => (attempt += 1)} />
  {:else}
    <div role="status" aria-busy="true">
      <span class="sr-only">{t('load.loading')}</span>
      <Skeleton height="200px" radius="var(--radius-lg)" />
    </div>
  {/if}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .help {
    margin-bottom: 14px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  label {
    display: block;
    margin: 0 0 6px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .line {
    display: flex;
    gap: 8px;
  }
  input {
    box-sizing: border-box;
    flex: 1 1 auto;
    min-width: 0;
    height: 48px;
    padding: 0 14px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-mono);
    font-size: 14px;
    line-height: 1;
    color-scheme: dark;
  }
  input:focus-visible {
    border-color: var(--accent);
  }
  .line :global(.btn) {
    flex: 0 0 auto;
    min-width: 76px;
    border-radius: var(--radius-sm);
    font-size: 14px;
  }
  .small {
    margin-top: 6px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .err {
    margin-top: 8px;
  }
  .submit {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 14px;
    margin-top: 14px;
  }
  .saved {
    font-size: 13.5px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-2);
  }
</style>
