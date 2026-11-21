<script lang="ts">
  import { onMount } from 'svelte';
  import Field from '../../lib/accounts/Field.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import CheckField from '../../lib/ui/CheckField.svelte';
  import ConfirmDialog from '../../lib/ui/ConfirmDialog.svelte';
  import FormSheet from '../../lib/ui/FormSheet.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { ApiError } from '../../lib/core/api';
  import { failureText } from '../../lib/core/catalog';
  import { connectionStatus } from '../../lib/core/connectionWords';
  import { t } from '../../lib/core/i18n';
  import {
    createSource,
    deleteSource,
    importSources,
    listSources,
    testSource,
    updateSource,
    type Source,
    type SourceInput,
    type SourceKind,
  } from '../../lib/core/sourcesApi';
  import type { PanelRow } from '../../lib/core/views';
  import ConnectionPanel from './ConnectionPanel.svelte';

  interface Props {
    /** Open the add dialog at once (a link from the releases panel). */
    openAdd?: boolean;
    onnote?: (message: string) => void;
  }

  let { openAdd: startAdd = false, onnote }: Props = $props();

  const KINDS: Array<{ value: SourceKind; label: string }> = [
    { value: 'torznab', label: 'Torznab' },
    { value: 'newznab', label: 'Newznab' },
  ];
  const kindName = (kind: string): string => KINDS.find((k) => k.value === kind)?.label ?? kind;

  let sources = $state.raw<Source[]>([]);
  let loaded = $state(false);
  let failed = $state(false);

  async function load(): Promise<void> {
    try {
      sources = await listSources();
      loaded = true;
      failed = false;
    } catch {
      if (!loaded) failed = true;
    }
  }

  onMount(() => {
    void load();
  });

  // ---- test ----
  let testing = $state<number | null>(null);
  let results = $state<Record<number, { ok: boolean; text: string }>>({});

  async function test(source: Source): Promise<void> {
    if (testing !== null) return;
    testing = source.id;
    delete results[source.id];
    try {
      const res = await testSource(source.id);
      results[source.id] = res.ok
        ? { ok: true, text: t('sources.test.ok', { ms: res.ms }) }
        : { ok: false, text: res.message || t('apps.test.failed') };
      void load();
    } catch (err) {
      results[source.id] = { ok: false, text: failureText(err) };
    } finally {
      testing = null;
    }
  }

  // ---- add and edit ----
  let formOpen = $state(false);
  let editing = $state<Source | undefined>();
  let kind = $state<SourceKind>('torznab');
  let name = $state('');
  let url = $state('');
  let apiKey = $state('');
  let enabled = $state(true);
  let busy = $state(false);
  let formError = $state('');
  let urlError = $state('');
  let nameError = $state('');

  function openForm(): void {
    editing = undefined;
    kind = 'torznab';
    name = '';
    url = '';
    apiKey = '';
    enabled = true;
    formError = urlError = nameError = '';
    formOpen = true;
  }

  function openEdit(source: Source): void {
    editing = source;
    kind = source.kind;
    name = source.name;
    url = source.url;
    apiKey = source.apiKey; // masked: sending it back keeps the stored one
    enabled = source.enabled;
    formError = urlError = nameError = '';
    formOpen = true;
  }

  let handledAdd = false;
  $effect(() => {
    if (startAdd && !handledAdd && loaded) {
      handledAdd = true;
      openForm();
    }
  });

  async function save(): Promise<void> {
    if (busy) return;
    nameError = urlError = '';
    if (!name.trim()) nameError = t('sources.error.name');
    if (!url.trim()) urlError = t('apps.error.url');
    if (nameError || urlError) return;
    const input: SourceInput = {
      name: name.trim(),
      url: url.trim(),
      apiKey,
      enabled,
      ...(editing ? {} : { kind }),
    };
    busy = true;
    formError = '';
    try {
      const saved = editing ? await updateSource(editing.id, input) : await createSource(input);
      formOpen = false;
      await load();
      onnote?.(t(editing ? 'sources.saved' : 'sources.added', { name: saved.name }));
      void test(saved);
    } catch (err) {
      formError =
        err instanceof ApiError && err.code === 'duplicate' ? t('sources.error.duplicate') : failureText(err);
    } finally {
      busy = false;
    }
  }

  // ---- remove (asks first) ----
  let removeOpen = $state(false);
  let removeBusy = $state(false);
  let removeError = $state('');

  async function confirmRemove(): Promise<void> {
    const source = editing;
    if (!source || removeBusy) return;
    removeBusy = true;
    removeError = '';
    try {
      await deleteSource(source.id);
      removeOpen = false;
      formOpen = false;
      await load();
      onnote?.(t('sources.removed', { name: source.name }));
    } catch (err) {
      removeError = failureText(err);
    } finally {
      removeBusy = false;
    }
  }

  // ---- import from Prowlarr or Jackett ----
  let importOpen = $state(false);
  let from = $state<'prowlarr' | 'jackett'>('prowlarr');
  let importUrl = $state('');
  let importKey = $state('');
  let importBusy = $state(false);
  let importError = $state('');
  let importUrlError = $state('');
  let importKeyError = $state('');

  function openImport(): void {
    from = 'prowlarr';
    importUrl = '';
    importKey = '';
    importError = importUrlError = importKeyError = '';
    importOpen = true;
  }

  async function runImport(): Promise<void> {
    if (importBusy) return;
    importUrlError = importKeyError = '';
    if (!importUrl.trim()) importUrlError = t('apps.error.url');
    if (!importKey.trim()) importKeyError = t('sources.import.error.key');
    if (importUrlError || importKeyError) return;
    importBusy = true;
    importError = '';
    try {
      const res = await importSources({ from, url: importUrl.trim(), apiKey: importKey.trim() });
      importOpen = false;
      await load();
      const skipped = res.skipped?.length ?? 0;
      onnote?.(
        skipped > 0
          ? t('sources.import.doneSkipped', { n: res.sources.length, skipped })
          : t('sources.import.done', { n: res.sources.length }),
      );
    } catch (err) {
      importError = failureText(err);
    } finally {
      importBusy = false;
    }
  }

  const rows = $derived<PanelRow[]>(
    sources.map((s) => {
      const st = connectionStatus(s);
      return {
        key: s.id,
        name: s.name,
        tag: kindName(s.kind),
        status: st.text,
        state: st.state,
        detail: s.url,
        result: results[s.id],
        buttons: [
          {
            id: 'test',
            label: t('settings.action.test'),
            busyLabel: t('apps.testing'),
            aria: t('settings.rowAria', { action: t('settings.action.test'), name: s.name }),
            busy: testing === s.id,
            onclick: () => void test(s),
          },
          {
            id: 'edit',
            label: t('settings.action.edit'),
            aria: t('settings.rowAria', { action: t('settings.action.edit'), name: s.name }),
            onclick: () => openEdit(s),
          },
        ],
      };
    }),
  );
</script>

{#if failed && !loaded}
  <LoadFailed onretry={() => void load()} />
{:else if !loaded}
  <div class="loading" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    <Skeleton height="150px" radius="var(--radius-lg)" />
  </div>
{:else}
  <ConnectionPanel
    title={t('settings.panel.sources')}
    {rows}
    empty={t('sources.empty')}
    note={t('settings.note.sources')}
    anchor="sources"
  >
    {#snippet action()}
      <span class="head-actions">
        <Button size="sm" onclick={openImport}>{t('sources.import')}</Button>
        <Button size="sm" variant="primary" onclick={openForm}>
          <Icon name="plus" size={16} stroke={2.2} />
          {t('settings.addSource')}
        </Button>
      </span>
    {/snippet}
  </ConnectionPanel>
{/if}

<FormSheet
  bind:open={formOpen}
  title={editing ? t('sources.edit.title', { name: editing.name }) : t('sources.add.title')}
  intro={t('sources.form.intro')}
  submitLabel={t('apps.save')}
  {busy}
  error={formError}
  onsubmit={save}
>
  {#if !editing}
    <SelectField label={t('apps.field.kind')} bind:value={kind} options={KINDS} help={t('sources.field.kindHelp')} />
  {/if}
  <Field
    label={t('apps.field.name')}
    bind:value={name}
    name="name"
    error={nameError || undefined}
    oninput={() => (nameError = '')}
  />
  <Field
    label={t('sources.field.url')}
    bind:value={url}
    type="url"
    name="url"
    help={t('sources.field.urlHelp')}
    error={urlError || undefined}
    oninput={() => (urlError = '')}
  />
  <Field
    label={t('apps.field.apiKey')}
    bind:value={apiKey}
    type="password"
    name="apiKey"
    autocomplete="off"
    help={editing ? t('apps.field.secretHelp') : undefined}
  />
  <CheckField label={t('apps.field.enabled')} bind:checked={enabled} />
  {#snippet extra()}
    {#if editing}
      <Button
        size="lg"
        block
        onclick={() => {
          removeError = '';
          removeOpen = true;
        }}
      >
        <Icon name="trash" size={18} stroke={2} />
        {t('sources.remove')}
      </Button>
    {/if}
  {/snippet}
</FormSheet>

<FormSheet
  bind:open={importOpen}
  title={t('sources.import.title')}
  intro={t('sources.import.intro')}
  submitLabel={t('sources.import.submit')}
  busy={importBusy}
  error={importError}
  onsubmit={runImport}
>
  <SelectField
    label={t('sources.import.from')}
    bind:value={from}
    options={[
      { value: 'prowlarr', label: 'Prowlarr' },
      { value: 'jackett', label: 'Jackett' },
    ]}
  />
  <Field
    label={t('sources.import.url')}
    bind:value={importUrl}
    type="url"
    name="import-url"
    placeholder={from === 'prowlarr' ? 'http://127.0.0.1:9696' : 'http://127.0.0.1:9117'}
    error={importUrlError || undefined}
    oninput={() => (importUrlError = '')}
  />
  <Field
    label={t('sources.import.key')}
    bind:value={importKey}
    type="password"
    name="import-key"
    autocomplete="off"
    help={t('sources.import.keyHelp')}
    error={importKeyError || undefined}
    oninput={() => (importKeyError = '')}
  />
</FormSheet>

<ConfirmDialog
  bind:open={removeOpen}
  title={t('sources.remove.title', { name: editing?.name ?? '' })}
  body={t('sources.remove.body')}
  confirmLabel={t('sources.remove.confirm')}
  busy={removeBusy}
  error={removeError}
  onconfirm={confirmRemove}
/>

<style>
  .loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .head-actions {
    display: inline-flex;
    flex-wrap: wrap;
    gap: 8px;
  }
</style>
