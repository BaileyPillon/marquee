<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Field from '../../lib/accounts/Field.svelte';
  import ErrorNote from '../../lib/accounts/ErrorNote.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import ConfirmDialog from '../../lib/ui/ConfirmDialog.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { ApiError } from '../../lib/core/api';
  import {
    addFolder,
    getFolders,
    removeFolder,
    startScan,
    type Folder,
    type FolderKind,
    type ScanEvent,
  } from '../../lib/core/catalog';
  import { formatBytes } from '../../lib/core/format';
  import { t, type MessageKey } from '../../lib/core/i18n';
  import { libraryIndex } from '../../lib/core/libraryIndex.svelte.ts';

  interface Props {
    /** The owner adds and removes folders; an admin can only look and scan. */
    canEdit: boolean;
  }

  let { canEdit }: Props = $props();

  const kindKey: Record<FolderKind, MessageKey> = {
    movie: 'folders.kind.movie',
    show: 'folders.kind.show',
    music: 'folders.kind.music',
  };

  const uid = $props.id();
  const headId = `folders-${uid}`;
  const addId = `folders-add-${uid}`;
  const kindId = `folders-kind-${uid}`;

  let folders = $state.raw<Folder[]>([]);
  let loaded = $state(false);
  let failed = $state(false);
  let attempt = $state(0);

  async function load(signal?: AbortSignal): Promise<void> {
    try {
      folders = await getFolders(signal);
      failed = false;
      loaded = true;
      // The server is the last word on what is scanning, whatever the stream said or missed.
      for (const f of folders) {
        if (f.scanning) scanning[f.id] ??= { seen: 0 };
        else if (scanning[f.id]) delete scanning[f.id];
      }
    } catch {
      if (signal?.aborted) return;
      failed = true;
    }
  }

  $effect(() => {
    void attempt;
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  });

  // ---- scanning: progress arrives on the event stream ----
  let scanning = $state<Record<number, { seen: number }>>({});
  let scanNote = $state('');
  let scanError = $state('');
  const scanningNow = $derived(Object.keys(scanning).length > 0);

  function nameOf(id: number): string {
    return folders.find((f) => f.id === id)?.label || t('folders.thisFolder');
  }

  function onScanEvent(ev: ScanEvent): void {
    if (ev.state === 'started') {
      scanning[ev.folderId] = { seen: 0 };
      scanNote = '';
      scanError = '';
    } else if (ev.state === 'progress') {
      scanning[ev.folderId] = { seen: ev.seen ?? 0 };
    } else if (ev.state === 'done') {
      delete scanning[ev.folderId];
      const s = ev.summary;
      scanNote = s
        ? t('folders.scan.done', {
            name: nameOf(ev.folderId),
            seen: s.seen,
            added: s.added,
            matched: s.matched,
          })
        : t('folders.scan.doneShort', { name: nameOf(ev.folderId) });
      void load();
      libraryIndex.reload();
    } else if (ev.state === 'failed') {
      delete scanning[ev.folderId];
      scanError = t('folders.scan.failed', { name: nameOf(ev.folderId) });
    }
  }

  onMount(() => {
    if (typeof EventSource === 'undefined') return;
    // Same origin, so the session cookie goes with it. The browser reconnects by itself.
    const source = new EventSource('/api/v1/events?topic=library.scan');
    source.addEventListener('library.scan', (event) => {
      try {
        onScanEvent(JSON.parse((event as MessageEvent<string>).data) as ScanEvent);
      } catch {
        // A frame we cannot read is skipped.
      }
    });
    return () => source.close();
  });

  // While something scans, ask the server every few seconds too: if the stream drops or a scan
  // finishes before this page hears of it, the list still catches up.
  $effect(() => {
    if (!scanningNow) return;
    const timer = window.setInterval(() => void load(), 3000);
    return () => window.clearInterval(timer);
  });

  let startingScan = $state(false);
  async function scanNow(): Promise<void> {
    if (startingScan || scanningNow) return;
    startingScan = true;
    scanNote = '';
    scanError = '';
    added = '';
    try {
      const res = await startScan();
      const ids = [...res.started, ...res.running];
      if (ids.length === 0) scanNote = t('folders.scan.none');
      // A small folder can be done before this answer arrives: the list says which are still running.
      else void load();
    } catch {
      scanError = t('folders.scan.couldNotStart');
    } finally {
      startingScan = false;
    }
  }

  const scanStatus = $derived.by(() => {
    const ids = Object.keys(scanning).map(Number);
    if (ids.length === 0) return '';
    const seen = ids.reduce((sum, id) => sum + (scanning[id]?.seen ?? 0), 0);
    return t('folders.scan.running', { seen });
  });

  // ---- add a folder ----
  let kind = $state<FolderKind>('movie');
  let path = $state('');
  let label = $state('');
  let adding = $state(false);
  let addError = $state('');
  let addField = $state<'path' | 'form'>('form');
  let pathField: Field | undefined = $state();
  let added = $state('');

  const addErrors: Record<string, MessageKey> = {
    invalid_kind: 'folders.error.invalid_kind',
    invalid_path: 'folders.error.invalid_path',
    path_not_found: 'folders.error.path_not_found',
    not_a_folder: 'folders.error.not_a_folder',
    folder_nested: 'folders.error.folder_nested',
    folder_exists: 'folders.error.folder_exists',
  };
  /** The errors that are about the path field, so the message sits under it. */
  const pathErrors = new Set([
    'invalid_path',
    'path_not_found',
    'not_a_folder',
    'folder_nested',
    'folder_exists',
  ]);

  async function add(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (adding) return;
    added = '';
    if (!path.trim()) {
      addField = 'path';
      addError = t('folders.error.path_required');
      pathField?.focus();
      return;
    }
    adding = true;
    addError = '';
    try {
      const folder = await addFolder({ kind, path: path.trim(), label: label.trim() });
      folders = [...folders, folder];
      loaded = true;
      added = t('folders.added', { name: folder.label || folder.path });
      path = '';
      label = '';
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'network';
      addError = t(addErrors[code] ?? (code === 'network' ? 'error.network' : 'error.generic'));
      addField = pathErrors.has(code) ? 'path' : 'form';
      if (addField === 'path') pathField?.focus();
    } finally {
      adding = false;
    }
  }

  // ---- remove a folder (asks first) ----
  let removing = $state<Folder | undefined>();
  let confirmOpen = $state(false);
  let removeBusy = $state(false);
  let removeError = $state('');
  let heading: HTMLElement | undefined = $state();

  function askRemove(folder: Folder): void {
    removing = folder;
    removeError = '';
    confirmOpen = true;
  }

  async function confirmRemove(): Promise<void> {
    if (!removing || removeBusy) return;
    removeBusy = true;
    removeError = '';
    const folder = removing;
    try {
      await removeFolder(folder.id);
      folders = folders.filter((f) => f.id !== folder.id);
      libraryIndex.reload();
      added = t('folders.removed', { name: folder.label || folder.path });
      confirmOpen = false;
      // The button that opened the dialog is gone with its row: keep keyboard focus on the panel.
      await tick();
      heading?.focus({ preventScroll: true });
    } catch (err) {
      removeError = err instanceof ApiError && err.code === 'not_found' ? t('folders.error.gone') : t('error.generic');
    } finally {
      removeBusy = false;
    }
  }

  function counts(f: Folder): string {
    const parts = [
      f.files === 1 ? t('folders.filesOne') : t('folders.files', { n: f.files }),
      t('folders.matched', { n: f.matched }),
      t('folders.unmatched', { n: f.unmatched }),
    ];
    if (f.missing > 0) parts.push(t('folders.missing', { n: f.missing }));
    return parts.join(' · ');
  }

  function space(f: Folder): string {
    if (f.freeBytes === null || f.totalBytes === null) return t('folders.spaceUnknown');
    return t('folders.space', { free: formatBytes(f.freeBytes), total: formatBytes(f.totalBytes) });
  }
</script>

<div class="grid">
  <Panel labelledby={headId} padding="18px">
    <SectionHead title={t('settings.tab.folders')} id={headId} size="md" align="center" gap={4} minHeight={36}>
      <Button size="sm" disabled={startingScan || scanningNow || folders.length === 0} onclick={scanNow}>
        <Icon name="refresh" size={16} stroke={2.2} />
        {scanningNow ? t('folders.scanning') : t('folders.scanNow')}
      </Button>
    </SectionHead>
    <!-- Not focusable by Tab; it takes focus after a row is removed so the page keeps its place. -->
    <h3 class="sr-only" tabindex="-1" bind:this={heading}>{t('folders.list')}</h3>

    <div class="live" aria-live="polite">
      {#if scanStatus}<p class="progress">{scanStatus}</p>{/if}
      {#if scanNote}<p class="note">{scanNote}</p>{/if}
      {#if added}<p class="note">{added}</p>{/if}
    </div>
    {#if scanError}<div class="err"><ErrorNote>{scanError}</ErrorNote></div>{/if}

    {#if failed && !loaded}
      <LoadFailed onretry={() => (attempt += 1)} />
    {:else if !loaded}
      <div class="loading" role="status" aria-busy="true">
        <span class="sr-only">{t('load.loading')}</span>
        <Skeleton height="64px" radius="var(--radius-md)" />
        <Skeleton height="64px" radius="var(--radius-md)" />
      </div>
    {:else if folders.length === 0}
      <p class="empty">{canEdit ? t('folders.empty.owner') : t('folders.empty.other')}</p>
    {:else}
      <ul class="list">
        {#each folders as folder (folder.id)}
          <li>
            <div class="text">
              <div class="name-row">
                <span class="name">{folder.label || folder.path}</span>
                <Chip tone="faint" size="micro">{t(kindKey[folder.kind])}</Chip>
                {#if scanning[folder.id]}<Chip tone="solid" size="micro">{t('folders.scanning')}</Chip>{/if}
              </div>
              <div class="path">{folder.path}</div>
              <div class="meta">{counts(folder)}</div>
              <div class="meta">{space(folder)}</div>
            </div>
            {#if canEdit}
              <Button
                size="compact"
                aria-label={t('folders.removeAria', { name: folder.label || folder.path })}
                onclick={() => askRemove(folder)}
              >
                {t('action.remove')}
              </Button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </Panel>

  {#if canEdit}
    <Panel labelledby={addId} padding="18px">
      <SectionHead title={t('folders.add.heading')} id={addId} size="md" align="center" gap={6} minHeight={36} />
      <p class="help">{t('folders.add.help')}</p>
      <form class="form" onsubmit={add} novalidate>
        <div class="field">
          <label for={kindId}>{t('folders.add.kind')}</label>
          <select id={kindId} bind:value={kind}>
            <option value="movie">{t(kindKey.movie)}</option>
            <option value="show">{t(kindKey.show)}</option>
            <option value="music">{t(kindKey.music)}</option>
          </select>
        </div>
        <Field
          bind:this={pathField}
          label={t('folders.add.path')}
          bind:value={path}
          name="path"
          help={t('folders.add.pathHelp')}
          error={addField === 'path' ? addError : undefined}
          oninput={() => {
            if (addField === 'path') addError = '';
          }}
        />
        <Field label={t('folders.add.label')} bind:value={label} name="label" help={t('folders.add.labelHelp')} />
        {#if addError && addField === 'form'}<div class="err"><ErrorNote>{addError}</ErrorNote></div>{/if}
        <div class="submit">
          <Button variant="primary" type="submit" disabled={adding}>
            <Icon name="plus" size={18} stroke={2.2} />
            {adding ? t('folders.adding') : t('folders.add.submit')}
          </Button>
        </div>
      </form>
    </Panel>
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmOpen}
  title={t('folders.remove.title', { name: removing ? removing.label || removing.path : '' })}
  body={t('folders.remove.body')}
  confirmLabel={t('folders.remove.confirm')}
  busy={removeBusy}
  error={removeError}
  onconfirm={confirmRemove}
/>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .live {
    min-height: 0;
  }
  .progress,
  .note {
    margin: 6px 0 0;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-2);
  }
  .progress {
    font-weight: 600;
    color: var(--text);
  }
  .err {
    margin-top: 8px;
  }
  .loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 12px;
  }
  .empty {
    margin-top: 10px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .list {
    margin-top: 8px;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px 14px;
    padding: 14px 0;
  }
  li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  li :global(.btn) {
    padding: 0 16px;
  }
  .text {
    flex: 1 1 220px;
    min-width: 0;
  }
  .name-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
  }
  .name {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .path {
    margin-top: 3px;
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 18px;
    color: var(--text-2);
    overflow-wrap: anywhere;
  }
  .meta {
    margin-top: 2px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }

  .help {
    margin-bottom: 14px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .form {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .field label {
    display: block;
    margin: 0 0 6px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  select {
    box-sizing: border-box;
    width: 100%;
    height: 48px;
    padding: 0 12px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 15px;
    color-scheme: dark;
  }
  .submit {
    margin-top: 2px;
  }
</style>
