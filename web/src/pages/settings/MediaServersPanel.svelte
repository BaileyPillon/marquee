<script lang="ts">
  import { onMount } from 'svelte';
  import Field from '../../lib/accounts/Field.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import CheckField from '../../lib/ui/CheckField.svelte';
  import ConfirmDialog from '../../lib/ui/ConfirmDialog.svelte';
  import FormSheet from '../../lib/ui/FormSheet.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import NotSetUp from '../../lib/ui/NotSetUp.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { ApiError } from '../../lib/core/api';
  import { failureText, isRouteMissing } from '../../lib/core/catalog';
  import { connectionStatus } from '../../lib/core/connectionWords';
  import { t } from '../../lib/core/i18n';
  import {
    addMediaServer,
    deleteMediaServer,
    listMediaServers,
    testMediaServer,
    updateMediaServer,
    type MediaServer,
    type MediaServerInput,
    type MediaServerKind,
    type PathRule,
  } from '../../lib/core/serversApi';
  import type { PanelRow } from '../../lib/core/views';
  import ConnectionPanel from './ConnectionPanel.svelte';

  interface Props {
    onnote?: (message: string) => void;
  }

  let { onnote }: Props = $props();

  const KINDS: Array<{ value: MediaServerKind; label: string }> = [
    { value: 'jellyfin', label: 'Jellyfin' },
    { value: 'emby', label: 'Emby' },
    { value: 'plex', label: 'Plex' },
  ];
  const kindName = (kind: string): string => KINDS.find((k) => k.value === kind)?.label ?? kind;
  const placeholders: Record<string, string> = {
    jellyfin: 'http://127.0.0.1:8096',
    emby: 'http://127.0.0.1:8096',
    plex: 'http://127.0.0.1:32400',
  };

  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let status = $state<Status>('loading');
  let servers = $state.raw<MediaServer[]>([]);

  async function load(): Promise<void> {
    try {
      servers = await listMediaServers();
      status = 'ready';
    } catch (err) {
      if (isRouteMissing(err)) status = 'off';
      else if (status !== 'ready') status = 'failed';
    }
  }

  onMount(() => {
    void load();
  });

  // ---- test ----
  let testing = $state<number | null>(null);
  let results = $state<Record<number, { ok: boolean; text: string }>>({});

  async function test(server: MediaServer): Promise<void> {
    if (testing !== null) return;
    testing = server.id;
    delete results[server.id];
    try {
      const res = await testMediaServer(server.id);
      const libraries = res.libraries?.length ?? 0;
      results[server.id] = res.ok
        ? {
            ok: true,
            text: [
              res.version ? t('apps.test.okVersion', { version: res.version }) : t('apps.test.ok'),
              t('servers.test.libraries', { n: libraries }),
            ].join(' · '),
          }
        : { ok: false, text: res.error || t('apps.test.failed') };
      void load();
    } catch (err) {
      results[server.id] = { ok: false, text: failureText(err) };
    } finally {
      testing = null;
    }
  }

  // ---- add and edit ----
  let formOpen = $state(false);
  let editing = $state<MediaServer | undefined>();
  let kind = $state<MediaServerKind>('jellyfin');
  let name = $state('');
  let url = $state('');
  let token = $state('');
  let enabled = $state(true);
  let rules = $state<PathRule[]>([]);
  let busy = $state(false);
  let formError = $state('');
  let urlError = $state('');
  let nameTouched = false;

  $effect(() => {
    if (editing || nameTouched) return;
    name = kindName(kind);
  });

  function openAdd(): void {
    editing = undefined;
    kind = 'jellyfin';
    nameTouched = false;
    name = kindName(kind);
    url = '';
    token = '';
    enabled = true;
    rules = [];
    formError = urlError = '';
    formOpen = true;
  }

  function openEdit(server: MediaServer): void {
    editing = server;
    kind = server.kind;
    nameTouched = true;
    name = server.name;
    url = server.url;
    token = server.token; // masked: sending it back keeps the stored one
    enabled = server.enabled;
    rules = (server.pathMap ?? []).map((r) => ({ ...r }));
    formError = urlError = '';
    formOpen = true;
  }

  function addRule(): void {
    rules = [...rules, { from: '', to: '' }];
  }

  function removeRule(index: number): void {
    rules = rules.filter((_, i) => i !== index);
  }

  async function save(): Promise<void> {
    if (busy) return;
    urlError = '';
    if (!url.trim()) {
      urlError = t('apps.error.url');
      return;
    }
    const input: MediaServerInput = {
      name: name.trim(),
      url: url.trim(),
      token,
      enabled,
      // A rule with nothing in it is just an empty row the owner left behind.
      pathMap: rules.filter((r) => r.from.trim() || r.to.trim()).map((r) => ({ from: r.from.trim(), to: r.to.trim() })),
      ...(editing ? {} : { kind }),
    };
    busy = true;
    formError = '';
    try {
      const saved = editing ? await updateMediaServer(editing.id, input) : await addMediaServer(input);
      formOpen = false;
      await load();
      onnote?.(t(editing ? 'servers.saved' : 'servers.added', { name: saved.name }));
      void test(saved);
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

  async function confirmRemove(): Promise<void> {
    const server = editing;
    if (!server || removeBusy) return;
    removeBusy = true;
    removeError = '';
    try {
      await deleteMediaServer(server.id);
      removeOpen = false;
      formOpen = false;
      await load();
      onnote?.(t('servers.removed', { name: server.name }));
    } catch (err) {
      removeError = failureText(err);
    } finally {
      removeBusy = false;
    }
  }

  const rows = $derived<PanelRow[]>(
    servers.map((s) => {
      const st = connectionStatus(s);
      return {
        key: s.id,
        name: s.name,
        ...(kindName(s.kind).toLowerCase() !== s.name.toLowerCase() ? { tag: kindName(s.kind) } : {}),
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

  const uid = $props.id();
  const offId = `servers-off-${uid}`;
</script>

{#if status === 'off'}
  <Panel labelledby={offId} padding="18px">
    <SectionHead title={t('settings.panel.servers')} id={offId} size="md" align="center" gap={8} minHeight={36} />
    <NotSetUp hint={t('servers.offHint')} />
  </Panel>
{:else if status === 'failed'}
  <LoadFailed onretry={() => void load()} />
{:else if status === 'loading'}
  <div class="loading" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    <Skeleton height="150px" radius="var(--radius-lg)" />
  </div>
{:else}
  <ConnectionPanel
    title={t('settings.panel.servers')}
    {rows}
    empty={t('servers.empty')}
    note={t('servers.note')}
  >
    {#snippet action()}
      <Button size="sm" onclick={openAdd}>
        <Icon name="plus" size={16} stroke={2.2} />
        {t('servers.add')}
      </Button>
    {/snippet}
  </ConnectionPanel>
{/if}

<FormSheet
  bind:open={formOpen}
  title={editing ? t('servers.edit.title', { name: editing.name }) : t('servers.add.title')}
  intro={t('servers.form.intro')}
  submitLabel={t('apps.save')}
  {busy}
  error={formError}
  onsubmit={save}
>
  {#if !editing}
    <SelectField label={t('apps.field.kind')} bind:value={kind} options={KINDS} />
  {/if}
  <Field label={t('apps.field.name')} bind:value={name} name="name" oninput={() => (nameTouched = true)} />
  <Field
    label={t('apps.field.url')}
    bind:value={url}
    type="url"
    name="url"
    placeholder={placeholders[kind] ?? ''}
    error={urlError || undefined}
    oninput={() => (urlError = '')}
  />
  <Field
    label={kind === 'plex' ? t('servers.field.plexToken') : t('servers.field.apiKey')}
    bind:value={token}
    type="password"
    name="token"
    autocomplete="off"
    help={editing
      ? t('apps.field.secretHelp')
      : kind === 'plex'
        ? t('servers.field.plexTokenHelp')
        : t('servers.field.apiKeyHelp')}
  />
  <CheckField label={t('apps.field.enabled')} bind:checked={enabled} />

  <fieldset class="rules">
    <legend>{t('servers.rules.heading')}</legend>
    <p class="rules-help">{t('servers.rules.help')}</p>
    {#each rules as rule, i (i)}
      <div class="rule">
        <Field label={t('servers.rules.from')} bind:value={rule.from} name="from-{i}" />
        <Field label={t('servers.rules.to')} bind:value={rule.to} name="to-{i}" />
        <Button size="compact" aria-label={t('servers.rules.removeAria', { n: i + 1 })} onclick={() => removeRule(i)}>
          {t('action.remove')}
        </Button>
      </div>
    {/each}
    <div class="rules-add">
      <Button size="compact" onclick={addRule}>
        <Icon name="plus" size={16} stroke={2.2} />
        {t('servers.rules.add')}
      </Button>
    </div>
  </fieldset>

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
        {t('servers.remove')}
      </Button>
    {/if}
  {/snippet}
</FormSheet>

<ConfirmDialog
  bind:open={removeOpen}
  title={t('servers.remove.title', { name: editing?.name ?? '' })}
  body={t('servers.remove.body')}
  confirmLabel={t('servers.remove.confirm')}
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
  .rules {
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }
  .rules legend {
    padding: 0;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .rules-help {
    margin: 4px 0 10px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .rule {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    margin-bottom: 14px;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--hairline-strong);
  }
  .rule > :global(.field) {
    width: 100%;
  }
</style>
