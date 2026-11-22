<script lang="ts">
  import { onMount } from 'svelte';
  import Field from '../../lib/accounts/Field.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import CheckField from '../../lib/ui/CheckField.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import ConfirmDialog from '../../lib/ui/ConfirmDialog.svelte';
  import CopyButton from '../../lib/ui/CopyButton.svelte';
  import FormSheet from '../../lib/ui/FormSheet.svelte';
  import BottomSheet from '../../lib/ui/BottomSheet.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import NotSetUp from '../../lib/ui/NotSetUp.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { failureText, isRouteMissing } from '../../lib/core/catalog';
  import { formatAgo, formatDay, formatWhen } from '../../lib/core/format';
  import { t, type MessageKey } from '../../lib/core/i18n';
  import {
    createApiKey,
    getMcpInfo,
    listApiKeys,
    revokeApiKey,
    type ApiKeyInfo,
    type McpInfo,
  } from '../../lib/core/serversApi';

  interface Props {
    onnote?: (message: string) => void;
  }

  let { onnote }: Props = $props();

  const uid = $props.id();
  const connectId = `claude-connect-${uid}`;
  const keysId = `claude-keys-${uid}`;
  const callsId = `claude-calls-${uid}`;
  const toolsId = `claude-tools-${uid}`;

  // ---- MCP info ----
  type Status = 'loading' | 'ready' | 'off' | 'failed';
  let infoStatus = $state<Status>('loading');
  let info = $state.raw<McpInfo | undefined>();

  async function loadInfo(): Promise<void> {
    try {
      info = await getMcpInfo();
      infoStatus = 'ready';
    } catch (err) {
      if (isRouteMissing(err)) infoStatus = 'off';
      else if (infoStatus !== 'ready') infoStatus = 'failed';
    }
  }

  // ---- keys ----
  let keysStatus = $state<'loading' | 'ready' | 'failed'>('loading');
  let keys = $state.raw<ApiKeyInfo[]>([]);
  let allowed = $state.raw<string[]>([]);

  async function loadKeys(): Promise<void> {
    try {
      const res = await listApiKeys();
      keys = res.keys;
      allowed = res.scopes;
      keysStatus = 'ready';
    } catch {
      if (keysStatus !== 'ready') keysStatus = 'failed';
    }
  }

  onMount(() => {
    void loadInfo();
    void loadKeys();
  });

  /** The endpoint to give Claude: what the server says, else this page's own address. */
  const endpoint = $derived(info?.url || `${location.origin}/mcp`);
  const snippet = (key: string): string =>
    `claude mcp add --transport http marquee ${endpoint} --header "Authorization: Bearer ${key}"`;

  /**
   * What the list says about a tool: the short sentence the server writes for people (`summary`).
   * The long description is written for Claude, so an older server that sends no summary gets its
   * first sentence instead.
   */
  function summaryOf(tool: { summary?: string; description: string }): string {
    if (tool.summary) return tool.summary;
    const text = tool.description;
    const end = text.search(/[.!?](\s|$)/);
    const sentence = end > 0 ? text.slice(0, end + 1) : text;
    return sentence.length > 160 ? `${sentence.slice(0, 157).trimEnd()}…` : sentence;
  }

  const ABILITIES = ['see', 'ask', 'downloads', 'settings', 'delete', 'approve', 'admin'] as const;
  type Ability = (typeof ABILITIES)[number];
  const abilityKey = (scope: string): MessageKey =>
    (ABILITIES as readonly string[]).includes(scope)
      ? (`settings.ability.${scope as Ability}` as MessageKey)
      : 'settings.ability.other';
  const DANGEROUS: Record<string, MessageKey> = {
    approve: 'keys.warn.approve',
    delete: 'keys.warn.delete',
    settings: 'keys.warn.settings',
    admin: 'keys.warn.admin',
  };

  // ---- new key ----
  let newOpen = $state(false);
  let newName = $state('');
  let ticks = $state<Record<string, boolean>>({});
  let newBusy = $state(false);
  let newError = $state('');
  let nameError = $state('');
  let secret = $state('');
  let secretOpen = $state(false);

  function openNew(): void {
    newName = '';
    ticks = Object.fromEntries(allowed.map((s) => [s, s === 'see' || s === 'ask']));
    newError = nameError = '';
    newOpen = true;
  }

  const ticked = $derived(allowed.filter((s) => ticks[s]));

  async function createKey(): Promise<void> {
    if (newBusy) return;
    nameError = '';
    if (!newName.trim()) {
      nameError = t('keys.error.name');
      return;
    }
    if (ticked.length === 0) {
      newError = t('keys.error.scopes');
      return;
    }
    newBusy = true;
    newError = '';
    try {
      const res = await createApiKey({ name: newName.trim(), scopes: ticked });
      secret = res.secret;
      newOpen = false;
      secretOpen = true;
      void loadKeys();
      void loadInfo();
    } catch (err) {
      newError = failureText(err);
    } finally {
      newBusy = false;
    }
  }

  function doneWithSecret(): void {
    secretOpen = false;
    secret = ''; // gone for good: the server never shows it again
  }

  // ---- revoke ----
  let revoking = $state<ApiKeyInfo | undefined>();
  let revokeOpen = $state(false);
  let revokeBusy = $state(false);
  let revokeError = $state('');

  function askRevoke(key: ApiKeyInfo): void {
    revoking = key;
    revokeError = '';
    revokeOpen = true;
  }

  async function confirmRevoke(): Promise<void> {
    const key = revoking;
    if (!key || revokeBusy) return;
    revokeBusy = true;
    revokeError = '';
    try {
      await revokeApiKey(key.id);
      revokeOpen = false;
      await loadKeys();
      onnote?.(t('keys.revoked', { name: key.name }));
    } catch (err) {
      revokeError = failureText(err);
    } finally {
      revokeBusy = false;
    }
  }

  function keyStatus(k: ApiKeyInfo): string {
    if (k.revokedAt) return t('keys.statusRevoked', { date: formatDay(k.revokedAt) });
    const created = t('keys.created', { date: formatDay(k.createdAt) });
    return k.lastUsedAt
      ? t('keys.createdUsed', { created, ago: formatAgo(k.lastUsedAt) })
      : t('keys.createdNever', { created });
  }
</script>

<div class="grid">
  <!-- Connect Claude -->
  <Panel labelledby={connectId} padding="18px">
    <SectionHead title={t('claude.connect.heading')} id={connectId} size="md" align="center" gap={4} minHeight={36} />
    {#if infoStatus === 'loading'}
      <div class="loading" role="status" aria-busy="true">
        <span class="sr-only">{t('load.loading')}</span>
        <Skeleton height="64px" radius="var(--radius-md)" />
      </div>
    {:else if infoStatus === 'off'}
      <NotSetUp hint={t('claude.offHint')} />
    {:else if infoStatus === 'failed'}
      <LoadFailed onretry={() => void loadInfo()} />
    {:else if info}
      <p class="help">{t('settings.note.mcp')}</p>
      {#if !info.enabled}
        <p class="problem" role="status">{t('claude.disabled')}</p>
      {/if}

      <h3 class="label">{t('claude.endpoint')}</h3>
      <div class="code-row">
        <code class="code">{endpoint}</code>
        <CopyButton text={endpoint} label={t('claude.copyEndpoint')} />
      </div>

      <h3 class="label">{t('claude.snippet')}</h3>
      <p class="help tight">{t('claude.snippetHelp')}</p>
      <div class="code-row">
        <code class="code block">{snippet('<key>')}</code>
        <CopyButton text={snippet('<key>')} label={t('claude.copySnippet')} />
      </div>
    {/if}
  </Panel>

  <!-- API keys -->
  <Panel labelledby={keysId} padding="18px">
    <SectionHead title={t('claude.keys.heading')} id={keysId} size="md" align="center" gap={4} minHeight={36}>
      <Button variant="primary" size="sm" disabled={keysStatus !== 'ready'} onclick={openNew}>
        <Icon name="plus" size={16} stroke={2.2} />
        {t('settings.newKey')}
      </Button>
    </SectionHead>
    {#if keysStatus === 'loading'}
      <div class="loading" role="status" aria-busy="true">
        <span class="sr-only">{t('load.loading')}</span>
        <Skeleton height="64px" radius="var(--radius-md)" />
      </div>
    {:else if keysStatus === 'failed'}
      <LoadFailed onretry={() => void loadKeys()} />
    {:else if keys.length === 0}
      <p class="help">{t('keys.empty')}</p>
    {:else}
      <ul class="keys">
        {#each keys as key (key.id)}
          <li class:revoked={!!key.revokedAt}>
            <div class="line">
              <div class="text">
                <div class="name">
                  {key.name}
                  <span class="prefix">{key.prefix}…</span>
                </div>
                <div class="status">{keyStatus(key)}</div>
              </div>
              {#if !key.revokedAt}
                <Button
                  size="compact"
                  aria-label={t('settings.rowAria', { action: t('settings.action.revoke'), name: key.name })}
                  onclick={() => askRevoke(key)}
                >
                  {t('settings.action.revoke')}
                </Button>
              {/if}
            </div>
            <ul class="abilities" aria-label={t('settings.abilities')}>
              {#each key.scopes as scope (scope)}
                <li>
                  <Chip tone="allowed">
                    {#snippet icon()}<Icon name="check" size={14} stroke={2.4} />{/snippet}
                    <span>{t(abilityKey(scope))}</span>
                    <span class="sr-only">{t('settings.allowed')}</span>
                  </Chip>
                </li>
              {/each}
            </ul>
          </li>
        {/each}
      </ul>
    {/if}
    <p class="help">{t('keys.note')}</p>
  </Panel>

  <!-- What Claude can do -->
  {#if infoStatus === 'ready' && info}
    <Panel labelledby={toolsId} padding="18px">
      <SectionHead title={t('claude.tools.heading')} id={toolsId} size="md" align="center" gap={4} minHeight={36} />
      {#if (info.tools ?? []).length === 0}
        <p class="help">{t('claude.tools.none')}</p>
      {:else}
        <ul class="tools">
          {#each info.tools ?? [] as tool (tool.name)}
            <li>
              <div class="tool-top">
                <code class="tool-name">{tool.name}</code>
                <Chip tone="faint" size="micro">{t(abilityKey(tool.scope))}</Chip>
              </div>
              <p class="tool-desc">{summaryOf(tool)}</p>
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>

    <Panel labelledby={callsId} padding="18px">
      <SectionHead title={t('claude.calls.heading')} id={callsId} size="md" align="center" gap={4} minHeight={36} />
      {#if (info.recentCalls ?? []).length === 0}
        <p class="help">{t('claude.calls.none')}</p>
      {:else}
        <ul class="calls">
          {#each info.recentCalls ?? [] as call, i (call.at + call.tool + i)}
            <li>
              <span class="call-when">{formatWhen(call.at)}</span>
              <code class="call-tool">{call.tool}</code>
              {#if call.userName}<span class="call-who">{call.userName}</span>{/if}
              {#if call.ok}
                <Chip tone="solid" size="micro">{t('claude.calls.ok')}</Chip>
              {:else}
                <Chip tone="inverse" size="micro">{t('claude.calls.failed')}</Chip>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>
  {/if}
</div>

<!-- A new key -->
<FormSheet
  bind:open={newOpen}
  title={t('keys.new.title')}
  intro={t('keys.new.intro')}
  submitLabel={t('keys.new.submit')}
  busy={newBusy}
  error={newError}
  onsubmit={createKey}
>
  <Field
    label={t('keys.field.name')}
    bind:value={newName}
    name="name"
    placeholder={t('keys.field.namePlaceholder')}
    error={nameError || undefined}
    oninput={() => (nameError = '')}
  />
  <fieldset class="ticks">
    <legend>{t('keys.field.scopes')}</legend>
    {#each allowed as scope (scope)}
      <div class="tick">
        <CheckField
          label={t(abilityKey(scope))}
          help={t(`keys.scope.${scope as Ability}` as MessageKey)}
          checked={ticks[scope] === true}
          onchange={(on) => (ticks[scope] = on)}
        />
        {#if DANGEROUS[scope] && ticks[scope]}
          <p class="warn" role="note">
            <Icon name="warning" size={16} stroke={2} />
            <span>{t(DANGEROUS[scope] as MessageKey)}</span>
          </p>
        {/if}
      </div>
    {/each}
  </fieldset>
</FormSheet>

<!-- The secret: shown once -->
<BottomSheet bind:open={secretOpen} title={t('keys.secret.title')} onclose={() => (secret = '')} wide>
  <p class="help">{t('keys.secret.intro')}</p>
  <div class="code-row">
    <code class="code block">{secret}</code>
    <CopyButton text={secret} label={t('keys.secret.copy')} />
  </div>
  <h3 class="label">{t('claude.snippet')}</h3>
  <div class="code-row">
    <code class="code block">{snippet(secret)}</code>
    <CopyButton text={snippet(secret)} label={t('claude.copySnippet')} />
  </div>
  <div class="done">
    <Button variant="primary" size="lg" block onclick={doneWithSecret}>{t('keys.secret.done')}</Button>
  </div>
</BottomSheet>

<ConfirmDialog
  bind:open={revokeOpen}
  title={t('keys.revoke.title', { name: revoking?.name ?? '' })}
  body={t('keys.revoke.body')}
  confirmLabel={t('keys.revoke.confirm')}
  busy={revokeBusy}
  error={revokeError}
  onconfirm={confirmRevoke}
/>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .loading {
    margin-top: 8px;
  }
  .help {
    margin-top: 10px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .help.tight {
    margin-top: 2px;
  }
  .problem {
    margin-top: 10px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--danger);
  }
  .label {
    margin: 16px 0 6px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .code-row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 8px;
  }
  .code {
    flex: 1 1 220px;
    min-width: 0;
    box-sizing: border-box;
    padding: 10px 12px;
    border-radius: var(--radius-sm);
    background: var(--input);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 18px;
    color: var(--text);
    overflow-wrap: anywhere;
    user-select: all;
  }
  .keys > li {
    padding: 14px 0;
  }
  .keys > li + li {
    border-top: 1px solid var(--hairline-strong);
  }
  .keys > li.revoked .name,
  .keys > li.revoked .abilities {
    opacity: 0.6;
  }
  .line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 10px 14px;
  }
  .line :global(.btn) {
    padding: 0 16px;
  }
  .text {
    flex: 1 1 220px;
    min-width: 0;
  }
  .name {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .prefix {
    margin-inline-start: 6px;
    font-family: var(--font-mono);
    font-size: 12.5px;
    font-weight: 400;
    color: var(--muted);
  }
  .status {
    margin-top: 3px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
  }
  .abilities {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 12px;
  }
  .tools li {
    padding: 10px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .tools li:first-child {
    border-top: 0;
  }
  .tool-top {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
  }
  .tool-name {
    font-family: var(--font-mono);
    font-size: 13.5px;
    font-weight: 600;
    color: var(--text);
  }
  .tool-desc {
    margin-top: 2px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
  }
  .calls li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 12px;
    padding: 10px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .calls li:first-child {
    border-top: 0;
  }
  .call-when {
    flex: 0 0 auto;
    min-width: 96px;
    font-size: 13px;
    font-weight: 500;
    color: var(--text-3);
  }
  .call-tool {
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--text);
  }
  .call-who {
    font-size: 13px;
    color: var(--text-3);
  }

  .ticks {
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }
  .ticks legend {
    margin-bottom: 8px;
    padding: 0;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .tick {
    margin-bottom: 12px;
  }
  .warn {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-block: 6px 0;
    margin-inline: 32px 0;
    font-size: 13px;
    line-height: 18px;
    color: var(--accent);
  }
  .warn :global(svg) {
    margin-top: 1px;
  }
  .done {
    margin-top: 18px;
  }
</style>
