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
  import {
    addClient,
    deleteClient,
    listClients,
    testClient,
    updateClient,
    type ClientInput,
    type DownloadClient,
  } from '../../lib/core/downloadsApi';
  import { t } from '../../lib/core/i18n';
  import type { PanelRow } from '../../lib/core/views';
  import ConnectionPanel from './ConnectionPanel.svelte';

  interface Props {
    /** Open the add dialog for this kind at once (a link from Downloads or a release search). */
    addKind?: string;
    /** The panel told the page something (a toast). */
    onnote?: (message: string) => void;
  }

  let { addKind, onnote }: Props = $props();

  const KINDS = [
    { value: 'qbittorrent', label: 'qBittorrent' },
    { value: 'deluge', label: 'Deluge' },
    { value: 'sabnzbd', label: 'SABnzbd' },
  ];
  const kindName = (kind: string): string => KINDS.find((k) => k.value === kind)?.label ?? kind;
  const placeholders: Record<string, string> = {
    qbittorrent: 'http://127.0.0.1:8080',
    deluge: 'http://127.0.0.1:8112',
    sabnzbd: 'http://127.0.0.1:8085',
  };

  let clients = $state.raw<DownloadClient[]>([]);
  let loaded = $state(false);
  let failed = $state(false);

  async function load(): Promise<void> {
    try {
      clients = await listClients();
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

  async function test(client: DownloadClient): Promise<void> {
    if (testing !== null) return;
    testing = client.id;
    delete results[client.id];
    try {
      const res = await testClient(client.id);
      results[client.id] = res.ok
        ? { ok: true, text: res.version ? t('apps.test.okVersion', { version: res.version }) : t('apps.test.ok') }
        : { ok: false, text: res.error || t('apps.test.failed') };
      void load();
    } catch (err) {
      results[client.id] = { ok: false, text: failureText(err) };
    } finally {
      testing = null;
    }
  }

  // ---- add and edit ----
  let formOpen = $state(false);
  let editing = $state<DownloadClient | undefined>();
  let kind = $state('qbittorrent');
  let name = $state('');
  let url = $state('');
  let username = $state('');
  let password = $state('');
  let apiKey = $state('');
  let category = $state('marquee');
  let savePath = $state('');
  let enabled = $state(true);
  let busy = $state(false);
  let formError = $state('');
  let urlError = $state('');
  let nameTouched = false;

  const usesLogin = $derived(kind === 'qbittorrent');
  const usesPassword = $derived(kind === 'qbittorrent' || kind === 'deluge');
  const usesKey = $derived(kind === 'sabnzbd');

  // A new app gets its kind's own name until the owner types another.
  $effect(() => {
    if (editing || nameTouched) return;
    name = kindName(kind);
  });

  function openAdd(forKind = 'qbittorrent'): void {
    editing = undefined;
    kind = KINDS.some((k) => k.value === forKind) ? forKind : 'qbittorrent';
    nameTouched = false;
    name = kindName(kind);
    url = '';
    username = '';
    password = '';
    apiKey = '';
    category = 'marquee';
    savePath = '';
    enabled = true;
    formError = urlError = '';
    formOpen = true;
  }

  function openEdit(client: DownloadClient): void {
    editing = client;
    kind = client.kind;
    nameTouched = true;
    name = client.name;
    url = client.url;
    username = client.username;
    password = client.password; // masked: sending it back keeps the stored one
    apiKey = client.apiKey;
    category = client.category;
    savePath = client.savePath;
    enabled = client.enabled;
    formError = urlError = '';
    formOpen = true;
  }

  // A link such as /settings?add=qbittorrent opens the add dialog straight away (once).
  let handledAdd = false;
  $effect(() => {
    if (addKind && !handledAdd && loaded) {
      handledAdd = true;
      openAdd(addKind === 'downloadApp' ? 'qbittorrent' : addKind);
    }
  });

  async function save(): Promise<void> {
    if (busy) return;
    urlError = '';
    if (!url.trim()) {
      urlError = t('apps.error.url');
      return;
    }
    const input: ClientInput = {
      name: name.trim(),
      url: url.trim(),
      category: category.trim(),
      savePath: savePath.trim(),
      enabled,
      ...(editing ? {} : { kind }),
      ...(usesLogin ? { username: username.trim() } : {}),
      ...(usesPassword ? { password } : {}),
      ...(usesKey ? { apiKey } : {}),
    };
    busy = true;
    formError = '';
    try {
      const saved = editing ? await updateClient(editing.id, input) : await addClient(input);
      formOpen = false;
      await load();
      onnote?.(t(editing ? 'apps.saved' : 'apps.added', { name: saved.name }));
      void test(saved); // say at once whether it works
    } catch (err) {
      formError =
        err instanceof ApiError && err.code === 'name_taken' ? t('apps.error.nameTaken') : failureText(err);
    } finally {
      busy = false;
    }
  }

  // ---- remove (asks first) ----
  let removeOpen = $state(false);
  let removeBusy = $state(false);
  let removeError = $state('');

  function askRemove(): void {
    removeError = '';
    removeOpen = true;
  }

  async function confirmRemove(): Promise<void> {
    const client = editing;
    if (!client || removeBusy) return;
    removeBusy = true;
    removeError = '';
    try {
      await deleteClient(client.id);
      removeOpen = false;
      formOpen = false;
      await load();
      onnote?.(t('apps.removed', { name: client.name }));
    } catch (err) {
      removeError = err instanceof ApiError && err.code === 'client_in_use' ? t('apps.error.inUse') : failureText(err);
    } finally {
      removeBusy = false;
    }
  }

  const rows = $derived<PanelRow[]>(
    clients.map((c) => {
      const s = connectionStatus(c);
      return {
        key: c.id,
        name: c.name,
        ...(kindName(c.kind).toLowerCase() !== c.name.toLowerCase() ? { tag: kindName(c.kind) } : {}),
        status: s.text,
        state: s.state,
        detail: c.url,
        result: results[c.id],
        buttons: [
          {
            id: 'test',
            label: t('settings.action.test'),
            busyLabel: t('apps.testing'),
            aria: t('settings.rowAria', { action: t('settings.action.test'), name: c.name }),
            busy: testing === c.id,
            onclick: () => void test(c),
          },
          {
            id: 'edit',
            label: t('settings.action.edit'),
            aria: t('settings.rowAria', { action: t('settings.action.edit'), name: c.name }),
            onclick: () => openEdit(c),
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
    title={t('settings.panel.apps')}
    {rows}
    empty={t('apps.empty')}
    note={t('apps.note')}
    anchor="download-apps"
  >
    {#snippet action()}
      <Button size="sm" onclick={() => openAdd()}>
        <Icon name="plus" size={16} stroke={2.2} />
        {t('apps.add')}
      </Button>
    {/snippet}
  </ConnectionPanel>
{/if}

<FormSheet
  bind:open={formOpen}
  title={editing ? t('apps.edit.title', { name: editing.name }) : t('apps.add.title')}
  intro={t('apps.form.intro')}
  submitLabel={t('apps.save')}
  {busy}
  error={formError}
  onsubmit={save}
>
  {#if !editing}
    <SelectField label={t('apps.field.kind')} bind:value={kind} options={KINDS} />
  {/if}
  <Field
    label={t('apps.field.name')}
    bind:value={name}
    name="name"
    oninput={() => (nameTouched = true)}
  />
  <Field
    label={t('apps.field.url')}
    bind:value={url}
    type="url"
    name="url"
    placeholder={placeholders[kind] ?? ''}
    help={t('apps.field.urlHelp')}
    error={urlError || undefined}
    oninput={() => (urlError = '')}
  />
  {#if usesLogin}
    <Field label={t('apps.field.username')} bind:value={username} name="username" autocomplete="off" />
  {/if}
  {#if usesPassword}
    <Field
      label={t('apps.field.password')}
      bind:value={password}
      type="password"
      name="password"
      autocomplete="off"
      help={editing ? t('apps.field.secretHelp') : undefined}
    />
  {/if}
  {#if usesKey}
    <Field
      label={t('apps.field.apiKey')}
      bind:value={apiKey}
      type="password"
      name="apiKey"
      autocomplete="off"
      help={editing ? t('apps.field.secretHelp') : t('apps.field.apiKeyHelp')}
    />
  {/if}
  <Field
    label={t('apps.field.category')}
    bind:value={category}
    name="category"
    help={t('apps.field.categoryHelp')}
  />
  <Field
    label={t('apps.field.savePath')}
    bind:value={savePath}
    name="savePath"
    help={t('apps.field.savePathHelp')}
  />
  <CheckField label={t('apps.field.enabled')} bind:checked={enabled} />
  {#snippet extra()}
    {#if editing}
      <Button size="lg" block onclick={askRemove}>
        <Icon name="trash" size={18} stroke={2} />
        {t('apps.remove')}
      </Button>
    {/if}
  {/snippet}
</FormSheet>

<ConfirmDialog
  bind:open={removeOpen}
  title={t('apps.remove.title', { name: editing?.name ?? '' })}
  body={t('apps.remove.body')}
  confirmLabel={t('apps.remove.confirm')}
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
</style>
