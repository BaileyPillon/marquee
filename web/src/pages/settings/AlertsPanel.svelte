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
  import { formatList, t, type MessageKey } from '../../lib/core/i18n';
  import {
    ALERT_EVENTS,
    ALERT_KINDS,
    createAlert,
    deleteAlert,
    listAlerts,
    testAlert,
    updateAlert,
    type AlertChannel,
    type AlertKind,
  } from '../../lib/core/serversApi';
  import type { PanelRow } from '../../lib/core/views';
  import ConnectionPanel from './ConnectionPanel.svelte';

  interface Props {
    onnote?: (message: string) => void;
  }

  let { onnote }: Props = $props();

  interface FieldSpec {
    name: string;
    label: MessageKey;
    secret?: boolean;
    placeholder?: string;
    help?: MessageKey;
    options?: Array<{ value: string; label: string }>;
  }

  // What each kind of channel needs (the server checks these again).
  const FIELDS: Record<AlertKind, FieldSpec[]> = {
    discord: [{ name: 'url', label: 'alerts.field.webhookUrl', secret: true, help: 'alerts.field.discordHelp' }],
    ntfy: [
      { name: 'server', label: 'alerts.field.server', placeholder: 'https://ntfy.sh' },
      { name: 'topic', label: 'alerts.field.topic' },
      { name: 'token', label: 'alerts.field.tokenOptional', secret: true },
    ],
    gotify: [
      { name: 'server', label: 'alerts.field.server', placeholder: 'http://127.0.0.1:8080' },
      { name: 'token', label: 'alerts.field.appToken', secret: true },
    ],
    telegram: [
      { name: 'botToken', label: 'alerts.field.botToken', secret: true, help: 'alerts.field.botTokenHelp' },
      { name: 'chatId', label: 'alerts.field.chatId', help: 'alerts.field.chatIdHelp' },
    ],
    pushover: [
      { name: 'appToken', label: 'alerts.field.appToken', secret: true },
      { name: 'userKey', label: 'alerts.field.userKey', secret: true },
    ],
    email: [
      { name: 'host', label: 'alerts.field.smtpHost', placeholder: 'smtp.example.com' },
      { name: 'port', label: 'alerts.field.smtpPort', placeholder: '587' },
      {
        name: 'security',
        label: 'alerts.field.security',
        options: [
          { value: 'starttls', label: 'STARTTLS' },
          { value: 'tls', label: 'TLS' },
          { value: 'none', label: 'None' },
        ],
      },
      { name: 'username', label: 'alerts.field.smtpUser' },
      { name: 'password', label: 'alerts.field.smtpPassword', secret: true },
      { name: 'from', label: 'alerts.field.from', placeholder: 'marquee@example.com' },
      { name: 'to', label: 'alerts.field.to', help: 'alerts.field.toHelp' },
    ],
    webhook: [
      { name: 'url', label: 'alerts.field.webhookUrl', secret: true },
      { name: 'secret', label: 'alerts.field.signingSecret', secret: true, help: 'alerts.field.signingHelp' },
    ],
  };

  const kindLabels: Record<AlertKind, string> = {
    discord: 'Discord',
    ntfy: 'ntfy',
    gotify: 'Gotify',
    telegram: 'Telegram',
    pushover: 'Pushover',
    email: 'Email',
    webhook: 'Webhook',
  };
  const kindName = (kind: string): string => kindLabels[kind as AlertKind] ?? kind;
  const eventKey = (event: string): MessageKey => `alerts.event.${event}` as MessageKey;
  const DEFAULT_EVENTS = new Set(['request.created', 'title.available', 'download.failed', 'import.failed', 'health.problem']);

  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let status = $state<Status>('loading');
  let channels = $state.raw<AlertChannel[]>([]);

  async function load(): Promise<void> {
    try {
      channels = await listAlerts();
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

  async function test(channel: AlertChannel): Promise<void> {
    if (testing !== null) return;
    testing = channel.id;
    delete results[channel.id];
    try {
      const res = await testAlert(channel.id);
      const ok = res.ok !== false && !res.error;
      results[channel.id] = ok
        ? { ok: true, text: t('alerts.test.ok') }
        : { ok: false, text: res.error || res.message || t('alerts.test.failed') };
      void load();
    } catch (err) {
      results[channel.id] = { ok: false, text: failureText(err) };
    } finally {
      testing = null;
    }
  }

  // ---- add and edit ----
  let formOpen = $state(false);
  let editing = $state<AlertChannel | undefined>();
  let kind = $state<AlertKind>('discord');
  let name = $state('');
  let enabled = $state(true);
  let values = $state<Record<string, string>>({});
  let events = $state<Record<string, boolean>>({});
  let busy = $state(false);
  let formError = $state('');
  let nameError = $state('');
  let nameTouched = false;

  const specs = $derived(FIELDS[kind]);

  $effect(() => {
    if (editing || nameTouched) return;
    name = kindName(kind);
  });

  function defaults(forKind: AlertKind): Record<string, string> {
    const out: Record<string, string> = {};
    for (const f of FIELDS[forKind]) out[f.name] = '';
    if (forKind === 'ntfy') out['server'] = 'https://ntfy.sh';
    if (forKind === 'email') {
      out['port'] = '587';
      out['security'] = 'starttls';
    }
    return out;
  }

  function openAdd(): void {
    editing = undefined;
    kind = 'discord';
    nameTouched = false;
    name = kindName(kind);
    enabled = true;
    values = defaults(kind);
    events = Object.fromEntries(ALERT_EVENTS.map((e) => [e, DEFAULT_EVENTS.has(e)]));
    formError = nameError = '';
    formOpen = true;
  }

  function openEdit(channel: AlertChannel): void {
    editing = channel;
    kind = channel.kind as AlertKind;
    nameTouched = true;
    name = channel.name;
    enabled = channel.enabled;
    const base = defaults(kind);
    const config = channel.config ?? {};
    for (const key of Object.keys(base)) {
      const v = config[key];
      base[key] = typeof v === 'string' ? v : typeof v === 'number' ? String(v) : (base[key] ?? '');
    }
    values = base; // secrets are masked: sending them back keeps the stored ones
    const have = new Set(channel.events ?? []);
    events = Object.fromEntries(ALERT_EVENTS.map((e) => [e, have.has(e)]));
    formError = nameError = '';
    formOpen = true;
  }

  // Changing the kind of a new channel starts its settings over.
  function changeKind(): void {
    values = defaults(kind);
  }

  const chosenEvents = $derived(ALERT_EVENTS.filter((e) => events[e]));

  async function save(): Promise<void> {
    if (busy) return;
    nameError = '';
    if (!name.trim()) {
      nameError = t('alerts.error.name');
      return;
    }
    if (chosenEvents.length === 0) {
      formError = t('alerts.error.events');
      return;
    }
    busy = true;
    formError = '';
    try {
      const body = {
        name: name.trim(),
        config: Object.fromEntries(specs.map((f) => [f.name, values[f.name] ?? ''])),
        events: [...chosenEvents],
        enabled,
        ...(editing ? {} : { kind }),
      };
      const saved = editing ? await updateAlert(editing.id, body) : await createAlert(body);
      formOpen = false;
      await load();
      onnote?.(t(editing ? 'alerts.saved' : 'alerts.added', { name: saved.name }));
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
    const channel = editing;
    if (!channel || removeBusy) return;
    removeBusy = true;
    removeError = '';
    try {
      await deleteAlert(channel.id);
      removeOpen = false;
      formOpen = false;
      await load();
      onnote?.(t('alerts.removed', { name: channel.name }));
    } catch (err) {
      removeError = failureText(err);
    } finally {
      removeBusy = false;
    }
  }

  function eventsSummary(c: AlertChannel): string {
    const list = c.events ?? [];
    if (list.length === 0) return t('alerts.noEvents');
    return formatList(
      list.map((e) => t(`alerts.event.short.${e}` as MessageKey)),
      'unit',
    );
  }

  const rows = $derived<PanelRow[]>(
    channels.map((c) => {
      const st = connectionStatus(c);
      return {
        key: c.id,
        name: c.name,
        ...(kindName(c.kind).toLowerCase() !== c.name.toLowerCase() ? { tag: kindName(c.kind) } : {}),
        status: st.text,
        state: st.state,
        note: eventsSummary(c),
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

  const uid = $props.id();
  const offId = `alerts-off-${uid}`;
</script>

{#if status === 'off'}
  <Panel labelledby={offId} padding="18px">
    <SectionHead title={t('settings.panel.alerts')} id={offId} size="md" align="center" gap={8} minHeight={36} />
    <NotSetUp hint={t('alerts.offHint')} />
  </Panel>
{:else if status === 'failed'}
  <LoadFailed onretry={() => void load()} />
{:else if status === 'loading'}
  <div class="loading" role="status" aria-busy="true">
    <span class="sr-only">{t('load.loading')}</span>
    <Skeleton height="120px" radius="var(--radius-lg)" />
  </div>
{:else}
  <ConnectionPanel title={t('settings.panel.alerts')} {rows} empty={t('alerts.empty')} note={t('alerts.note')}>
    {#snippet action()}
      <Button size="sm" onclick={openAdd}>
        <Icon name="plus" size={16} stroke={2.2} />
        {t('alerts.add')}
      </Button>
    {/snippet}
  </ConnectionPanel>
{/if}

<FormSheet
  bind:open={formOpen}
  title={editing ? t('alerts.edit.title', { name: editing.name }) : t('alerts.add.title')}
  intro={t('alerts.form.intro')}
  submitLabel={t('apps.save')}
  {busy}
  error={formError}
  onsubmit={save}
>
  {#if !editing}
    <SelectField
      label={t('apps.field.kind')}
      bind:value={kind}
      options={ALERT_KINDS.map((k) => ({ value: k, label: kindName(k) }))}
      onchange={changeKind}
    />
  {/if}
  <Field
    label={t('apps.field.name')}
    bind:value={name}
    name="name"
    error={nameError || undefined}
    oninput={() => {
      nameTouched = true;
      nameError = '';
    }}
  />
  {#each specs as spec (kind + spec.name)}
    {#if spec.options}
      <SelectField
        label={t(spec.label)}
        bind:value={() => values[spec.name] ?? '', (v) => (values[spec.name] = v)}
        options={spec.options}
      />
    {:else}
      <Field
        label={t(spec.label)}
        bind:value={() => values[spec.name] ?? '', (v) => (values[spec.name] = v)}
        type={spec.secret ? 'password' : 'text'}
        name={spec.name}
        autocomplete="off"
        placeholder={spec.placeholder}
        help={spec.help ? t(spec.help) : spec.secret && editing ? t('apps.field.secretHelp') : undefined}
      />
    {/if}
  {/each}
  <fieldset class="events">
    <legend>{t('alerts.field.events')}</legend>
    {#each ALERT_EVENTS as event (event)}
      <CheckField
        label={t(eventKey(event))}
        checked={events[event] === true}
        onchange={(on) => (events[event] = on)}
      />
    {/each}
  </fieldset>
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
        {t('alerts.remove')}
      </Button>
    {/if}
  {/snippet}
</FormSheet>

<ConfirmDialog
  bind:open={removeOpen}
  title={t('alerts.remove.title', { name: editing?.name ?? '' })}
  body={t('alerts.remove.body')}
  confirmLabel={t('alerts.remove.confirm')}
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
  .events {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }
  .events legend {
    margin-bottom: 8px;
    padding: 0;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
</style>
