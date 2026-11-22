<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import ErrorNote from '../../lib/accounts/ErrorNote.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import Toggle from '../../lib/ui/Toggle.svelte';
  import { ApiError } from '../../lib/core/api';
  import { t } from '../../lib/core/i18n';
  import { COMMON_LANGUAGES, languageCode, languageName } from '../../lib/core/languageNames';
  import {
    getSubtitleStatus,
    loadSubtitleSettings,
    saveSubtitleSettings,
    testSubtitleProvider,
    type HearingImpaired,
    type SubtitleProviderStatus,
    type SubtitleSettings,
    type SubtitleStatus,
  } from '../../lib/core/subtitlesApi';

  interface Props {
    /** Only the owner reads and saves these settings. */
    isOwner: boolean;
    onnote?: (message: string) => void;
  }

  let { isOwner, onnote }: Props = $props();

  const uid = $props.id();
  const langId = `subs-lang-${uid}`;
  const prefId = `subs-pref-${uid}`;
  const osId = `subs-os-${uid}`;
  const podId = `subs-pod-${uid}`;

  // ---- loading ----
  let saved = $state.raw<SubtitleSettings | undefined>();
  let form = $state<SubtitleSettings | undefined>();
  let loadFailed = $state(false);
  let status = $state.raw<SubtitleStatus | undefined>();
  let attempt = $state(0);

  async function loadStatus(): Promise<void> {
    try {
      status = await getSubtitleStatus();
    } catch {
      // The status is a nicety: the form works without it.
    }
  }

  $effect(() => {
    if (!isOwner) return;
    void attempt;
    const controller = new AbortController();
    loadFailed = false;
    loadSubtitleSettings(controller.signal).then(
      (settings) => {
        saved = settings;
        form = structuredClone(settings);
        void loadStatus();
      },
      () => {
        if (!controller.signal.aborted) loadFailed = true;
      },
    );
    return () => controller.abort();
  });

  const dirty = $derived(!!form && !!saved && JSON.stringify(form) !== JSON.stringify(saved));

  // ---- languages ----
  const addable = $derived(
    COMMON_LANGUAGES.filter((code) => !form?.languages.includes(code)).map((code) => ({
      value: code,
      label: `${languageName(code)} (${languageCode(code)})`,
    })),
  );
  let picker = $state('');

  function addLanguage(): void {
    if (!form || !picker) return;
    form.languages = [...form.languages, picker];
    picker = '';
  }

  function removeLanguage(code: string): void {
    if (form) form.languages = form.languages.filter((l) => l !== code);
  }

  // ---- saving ----
  let saving = $state(false);
  let savedNote = $state('');
  let error = $state('');

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (!form || saving || !dirty) return;
    saving = true;
    error = '';
    savedNote = '';
    try {
      const fresh = await saveSubtitleSettings(form);
      saved = fresh;
      form = structuredClone(fresh);
      savedNote = t('subs.saved');
      onnote?.(t('subs.saved'));
      void loadStatus();
    } catch (err) {
      error = err instanceof ApiError && err.status === 0 ? t('error.network') : t('subs.saveFailed');
    } finally {
      saving = false;
    }
  }

  // ---- testing a provider ----
  let testing = $state<'opensubtitles' | 'podnapisi' | ''>('');
  let tested = $state<{ provider: string; ok: boolean; text: string } | undefined>();

  async function test(provider: 'opensubtitles' | 'podnapisi'): Promise<void> {
    if (!form || testing) return;
    testing = provider;
    tested = undefined;
    const name = t(provider === 'opensubtitles' ? 'subs.provider.opensubtitles' : 'subs.provider.podnapisi');
    try {
      await testSubtitleProvider(
        provider,
        provider === 'opensubtitles'
          ? { apiKey: form.opensubtitles.apiKey, username: form.opensubtitles.username, password: form.opensubtitles.password }
          : {},
      );
      tested = { provider, ok: true, text: t('subs.test.ok', { name }) };
    } catch (err) {
      tested = { provider, ok: false, text: err instanceof ApiError ? err.message : t('error.network') };
    } finally {
      testing = '';
      void loadStatus();
    }
  }

  // ---- how a provider is doing ----
  function providerOf(name: string): SubtitleProviderStatus | undefined {
    return status?.providers.find((p) => p.name === name);
  }

  function statusText(p: SubtitleProviderStatus | undefined): string {
    if (!p || !p.enabled) return t('subs.status.off');
    if (p.error) return p.error;
    if (p.quotaRemaining !== undefined && p.quotaRemaining !== null) return t('subs.status.quota', { n: p.quotaRemaining });
    return t('subs.status.ok');
  }

  const hiOptions = $derived([
    { value: 'prefer-not', label: t('subs.hi.preferNot') },
    { value: 'prefer', label: t('subs.hi.prefer') },
    { value: 'only', label: t('subs.hi.only') },
  ]);
  let showKey = $state(false);
</script>

{#if !isOwner}
  <Panel tag="div" padding="18px">
    <p class="muted">{t('subs.ownerOnly')}</p>
  </Panel>
{:else if loadFailed}
  <LoadFailed onretry={() => (attempt += 1)} />
{:else if !form}
  <div role="status" aria-busy="true">
    <span class="sr-only">{t('subs.loading')}</span>
    <Skeleton height="260px" radius="var(--radius-lg)" />
  </div>
{:else}
  <form class="grid" onsubmit={save} novalidate>
    <Panel labelledby={langId} padding="18px">
      <SectionHead title={t('subs.heading')} id={langId} size="md" align="center" gap={6} minHeight={36} />
      <p class="help">{t('subs.intro')}</p>

      <h3 class="small-head">{t('subs.languages.heading')}</h3>
      <p class="help tight">{t('subs.languages.help')}</p>
      {#if form.languages.length === 0}
        <p class="warn">{t('subs.languages.none')}</p>
      {:else}
        <ul class="langs">
          {#each form.languages as code (code)}
            <li class="lang">
              <span class="code">{languageCode(code)}</span>
              <span class="name">{languageName(code)}</span>
              <button
                type="button"
                class="x"
                aria-label={t('subs.languages.remove', { name: languageName(code) })}
                onclick={() => removeLanguage(code)}
              >
                <span aria-hidden="true">×</span>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="add">
        <SelectField
          label={t('subs.languages.add')}
          bind:value={picker}
          options={[{ value: '', label: t('subs.languages.choose') }, ...addable]}
          onchange={addLanguage}
        />
      </div>

      <div class="pref">
        <SelectField
          label={t('subs.hi.label')}
          bind:value={() => form?.hearingImpaired ?? 'prefer-not', (v) => form && (form.hearingImpaired = v as HearingImpaired)}
          options={hiOptions}
        />
      </div>

      <div class="score">
        <label for="{prefId}-score">{t('subs.minScore.label')}</label>
        <div class="range">
          <input id="{prefId}-score" type="range" min="0" max="100" step="5" bind:value={form.minScore} />
          <output for="{prefId}-score">{form.minScore}</output>
        </div>
        <p class="help tight">{t('subs.minScore.help')}</p>
      </div>

      <div class="toggles">
        <Toggle
          label={t('subs.afterImport.label')}
          checked={form.searchAfterImport}
          onchange={(on) => (form && (form.searchAfterImport = on))}
        />
        <p class="help tight">{t('subs.afterImport.help')}</p>
      </div>

      <div class="rescan">
        <label for="{prefId}-hours">{t('subs.rescan.label')}</label>
        <input id="{prefId}-hours" type="number" min="1" max="720" bind:value={form.rescanEveryHours} />
        <p class="help tight">{t('subs.rescan.help')}</p>
      </div>
    </Panel>

    <div class="stack">
      <Panel labelledby={osId} padding="18px">
        <SectionHead title={t('subs.provider.opensubtitles')} id={osId} size="md" align="center" gap={6} minHeight={36}>
          <span class="status" class:bad={!!providerOf('opensubtitles')?.error}>{statusText(providerOf('opensubtitles'))}</span>
        </SectionHead>
        <p class="help">{t('subs.os.help')}</p>
        <Toggle
          label={t('subs.provider.use', { name: t('subs.provider.opensubtitles') })}
          checked={form.opensubtitles.enabled}
          onchange={(on) => (form && (form.opensubtitles.enabled = on))}
        />

        <div class="fields">
          <label for="{osId}-key">{t('subs.os.apiKey')}</label>
          <div class="line">
            <input
              id="{osId}-key"
              name="opensubtitlesKey"
              type={showKey ? 'text' : 'password'}
              bind:value={form.opensubtitles.apiKey}
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
            />
            <Button size="lg" aria-label={showKey ? t('info.hideAria') : t('info.showAria')} onclick={() => (showKey = !showKey)}>
              {showKey ? t('info.hide') : t('info.show')}
            </Button>
          </div>
          <label for="{osId}-user">{t('subs.os.username')}</label>
          <input
            id="{osId}-user"
            name="opensubtitlesUser"
            type="text"
            bind:value={form.opensubtitles.username}
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          />
          <label for="{osId}-pass">{t('subs.os.password')}</label>
          <input
            id="{osId}-pass"
            name="opensubtitlesPass"
            type="password"
            bind:value={form.opensubtitles.password}
            autocomplete="new-password"
            spellcheck="false"
          />
        </div>
        <div class="test">
          <Button size="sm" disabled={testing !== ''} onclick={() => test('opensubtitles')}>
            {testing === 'opensubtitles' ? t('subs.testing') : t('subs.test')}
          </Button>
          {#if tested?.provider === 'opensubtitles'}
            <span class="result" class:ok={tested.ok} role="status">{tested.text}</span>
          {/if}
        </div>
      </Panel>

      <Panel labelledby={podId} padding="18px">
        <SectionHead title={t('subs.provider.podnapisi')} id={podId} size="md" align="center" gap={6} minHeight={36}>
          <span class="status" class:bad={!!providerOf('podnapisi')?.error}>{statusText(providerOf('podnapisi'))}</span>
        </SectionHead>
        <p class="help">{t('subs.pod.help')}</p>
        <Toggle
          label={t('subs.provider.use', { name: t('subs.provider.podnapisi') })}
          checked={form.podnapisi.enabled}
          onchange={(on) => (form && (form.podnapisi.enabled = on))}
        />
        <div class="test">
          <Button size="sm" disabled={testing !== ''} onclick={() => test('podnapisi')}>
            {testing === 'podnapisi' ? t('subs.testing') : t('subs.test')}
          </Button>
          {#if tested?.provider === 'podnapisi'}
            <span class="result" class:ok={tested.ok} role="status">{tested.text}</span>
          {/if}
        </div>
      </Panel>

      {#if status}
        <p class="missing" role="status">
          {status.missingCount === 0 ? t('subs.missing.none') : t('subs.missing', { n: status.missingCount })}
        </p>
      {/if}

      {#if error}<ErrorNote>{error}</ErrorNote>{/if}
      <div class="submit">
        <Button variant="primary" type="submit" disabled={saving || !dirty}>
          {saving ? t('subs.saving') : t('subs.save')}
        </Button>
        <span class="saved" aria-live="polite">{savedNote}</span>
      </div>
    </div>
  </form>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .stack {
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
  }
  .muted {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .help {
    margin: 0 0 14px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  .help.tight {
    margin: 6px 0 0;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .warn {
    margin: 0 0 12px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-2);
  }
  .small-head {
    margin: 18px 0 4px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .pref {
    margin-top: 22px;
  }
  .langs {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin: 10px 0 14px;
    padding: 0;
    list-style: none;
  }
  .lang {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-height: 34px;
    padding-block: 0;
    padding-inline: 12px 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
    font-size: 13.5px;
    color: var(--text);
  }
  .code {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    color: var(--muted);
  }
  .x {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: transparent;
    color: var(--text-2);
    font-size: 18px;
    line-height: 1;
    cursor: pointer;
  }
  .x:hover {
    background: var(--hover);
    color: var(--text);
  }
  .add {
    max-width: 320px;
  }
  .score,
  .toggles,
  .rescan {
    margin-top: 20px;
  }
  label {
    display: block;
    margin: 0 0 6px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .range {
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .range input {
    flex: 1 1 auto;
    min-width: 0;
    accent-color: var(--accent);
  }
  output {
    min-width: 36px;
    font-size: 15px;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    color: var(--text);
    text-align: end;
  }
  input[type='text'],
  input[type='password'],
  input[type='number'] {
    box-sizing: border-box;
    width: 100%;
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
  input[type='number'] {
    max-width: 140px;
  }
  input:focus-visible {
    border-color: var(--accent);
  }
  .fields {
    display: flex;
    flex-direction: column;
    gap: 0;
    margin-top: 16px;
  }
  .fields label:not(:first-child) {
    margin-top: 12px;
  }
  .line {
    display: flex;
    gap: 8px;
  }
  .line :global(.btn) {
    flex: 0 0 auto;
    min-width: 76px;
    border-radius: var(--radius-sm);
    font-size: 14px;
  }
  .status {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
  }
  .status.bad {
    color: var(--text);
  }
  .test {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 14px;
    margin-top: 14px;
  }
  .result {
    font-size: 13.5px;
    line-height: 18px;
    color: var(--text-2);
  }
  .result.ok {
    color: var(--text);
  }
  .missing {
    margin: 0;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  .submit {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 14px;
  }
  .saved {
    font-size: 13.5px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-2);
  }
</style>
