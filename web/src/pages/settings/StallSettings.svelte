<script lang="ts">
  import { onMount } from 'svelte';
  import ErrorNote from '../../lib/accounts/ErrorNote.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import CheckField from '../../lib/ui/CheckField.svelte';
  import { ApiError } from '../../lib/core/api';
  import {
    MAX_STALL_MINUTES,
    loadDownloadSettings,
    saveDownloadSettings,
    type DownloadSettings,
  } from '../../lib/core/downloadsApi';
  import { t } from '../../lib/core/i18n';

  interface Props {
    /** The panel told the page something (a toast). */
    onnote?: (message: string) => void;
  }

  let { onnote }: Props = $props();

  const uid = $props.id();
  const minutesId = `stall-minutes-${uid}`;
  const unitId = `stall-unit-${uid}`;
  const helpId = `stall-help-${uid}`;

  // ---- loading ----
  let saved = $state.raw<DownloadSettings | undefined>();
  let minutes = $state<number | null>(null); // a number input gives null while it is empty
  let remove = $state(true);
  let loadFailed = $state(false);

  async function load(): Promise<void> {
    loadFailed = false;
    try {
      const s = await loadDownloadSettings();
      saved = s;
      minutes = s.stallMinutes;
      remove = s.removeStalled;
    } catch {
      loadFailed = true;
    }
  }

  onMount(() => {
    void load();
  });

  const dirty = $derived(!!saved && (minutes !== saved.stallMinutes || remove !== saved.removeStalled));

  // ---- saving ----
  let saving = $state(false);
  let error = $state('');

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (saving || !dirty || minutes === null) return;
    error = '';
    if (!Number.isInteger(minutes) || minutes < 0 || minutes > MAX_STALL_MINUTES) {
      error = t('apps.stall.error.minutes', { max: MAX_STALL_MINUTES });
      return;
    }
    saving = true;
    try {
      const fresh = await saveDownloadSettings({ stallMinutes: minutes, removeStalled: remove });
      saved = fresh;
      minutes = fresh.stallMinutes;
      remove = fresh.removeStalled;
      onnote?.(t('apps.stall.saved'));
    } catch (err) {
      error = err instanceof ApiError && err.status === 0 ? t('error.network') : t('apps.stall.saveFailed');
    } finally {
      saving = false;
    }
  }
</script>

<form class="stall" onsubmit={save} novalidate>
  <h3 class="small-head">{t('apps.stall.heading')}</h3>
  {#if loadFailed}
    <p class="help" role="alert">{t('load.failed')}</p>
    <Button size="sm" onclick={() => void load()}>{t('load.retry')}</Button>
  {:else if !saved}
    <p class="help" role="status">{t('load.loading')}</p>
  {:else}
    <p class="help">{t('apps.stall.intro')}</p>
    <label for={minutesId}>{t('apps.stall.label')}</label>
    <div class="minutes">
      <input
        id={minutesId}
        type="number"
        inputmode="numeric"
        min="0"
        max={MAX_STALL_MINUTES}
        step="1"
        bind:value={minutes}
        aria-describedby="{unitId} {helpId}"
      />
      <span id={unitId} class="unit">{t('apps.stall.unit', { n: minutes ?? 0 })}</span>
    </div>
    <p id={helpId} class="help tight">{t('apps.stall.help')}</p>
    <div class="remove">
      <CheckField
        label={t('apps.stall.remove')}
        help={t('apps.stall.removeHelp')}
        bind:checked={remove}
        disabled={minutes === 0}
      />
    </div>
    {#if error}<div class="error"><ErrorNote>{error}</ErrorNote></div>{/if}
    <div class="submit">
      <Button variant="primary" size="sm" type="submit" disabled={saving || !dirty}>
        {saving ? t('apps.stall.saving') : t('apps.stall.save')}
      </Button>
    </div>
  {/if}
</form>

<style>
  .stall {
    margin-top: 6px;
    padding-top: 4px;
    border-top: 1px solid var(--hairline-strong);
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
  label {
    display: block;
    margin: 0 0 6px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .minutes {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .unit {
    font-size: 14px;
    line-height: 20px;
    color: var(--text-2);
  }
  input[type='number'] {
    box-sizing: border-box;
    width: 100%;
    max-width: 120px;
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
  .remove {
    margin-top: 16px;
  }
  .error {
    margin-top: 12px;
  }
  .submit {
    margin-top: 16px;
  }
</style>
