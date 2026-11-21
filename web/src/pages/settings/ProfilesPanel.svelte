<script lang="ts">
  import { onMount } from 'svelte';
  import Field from '../../lib/accounts/Field.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import CheckField from '../../lib/ui/CheckField.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import FormSheet from '../../lib/ui/FormSheet.svelte';
  import LoadFailed from '../../lib/ui/LoadFailed.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { failureText } from '../../lib/core/catalog';
  import { formatDay } from '../../lib/core/format';
  import { t } from '../../lib/core/i18n';
  import {
    MUSIC_FORMATS,
    RESOLUTIONS,
    VIDEO_SOURCES,
    deleteBlocklistEntry,
    listBlocklist,
    listProfiles,
    updateProfile,
    type BlocklistEntry,
    type Profile,
  } from '../../lib/core/sourcesApi';

  interface Props {
    onnote?: (message: string) => void;
  }

  let { onnote }: Props = $props();

  const uid = $props.id();
  const listId = `profiles-${uid}`;
  const blockId = `blocklist-${uid}`;

  let profiles = $state.raw<Profile[]>([]);
  let loaded = $state(false);
  let failed = $state(false);

  async function load(): Promise<void> {
    try {
      profiles = await listProfiles();
      loaded = true;
      failed = false;
    } catch {
      if (!loaded) failed = true;
    }
  }

  // ---- the blocklist ----
  let blocked = $state.raw<BlocklistEntry[]>([]);
  let blockedTotal = $state(0);
  let blockLoaded = $state(false);
  let unblocking = $state<number | null>(null);

  async function loadBlocklist(): Promise<void> {
    try {
      const res = await listBlocklist();
      blocked = res.items;
      blockedTotal = res.total;
      blockLoaded = true;
    } catch {
      blockLoaded = true; // an empty list is a fine answer to "nothing to show"
    }
  }

  async function unblock(entry: BlocklistEntry): Promise<void> {
    if (unblocking !== null) return;
    unblocking = entry.id;
    try {
      await deleteBlocklistEntry(entry.id);
      blocked = blocked.filter((b) => b.id !== entry.id);
      blockedTotal = Math.max(0, blockedTotal - 1);
      onnote?.(t('quality.unblocked'));
    } catch (err) {
      onnote?.(failureText(err));
    } finally {
      unblocking = null;
    }
  }

  onMount(() => {
    void load();
    void loadBlocklist();
  });

  // ---- words ----
  const groups = $derived(
    (['movie', 'show', 'artist'] as const)
      .map((kind) => ({ kind, items: profiles.filter((p) => p.kind === kind) }))
      .filter((g) => g.items.length > 0),
  );

  const qualityWord = (p: Profile): string =>
    p.quality === 'uhd' ? t('ask.quality.uhd.short') : t('ask.quality.hd.short');

  const kindHeading = (kind: string): string =>
    kind === 'show' ? t('folders.kind.show') : kind === 'artist' ? t('folders.kind.music') : t('folders.kind.movie');

  /** "Movies · 4K": the same name appears under every kind, so the kind goes with it. */
  const labelOf = (p: Profile): string => `${kindHeading(p.kind)} · ${p.name}`;

  const gb = (mb: number): string => {
    const value = mb / 1024;
    return `${value >= 10 ? Math.round(value) : Math.round(value * 10) / 10} GB`;
  };

  function summary(p: Profile): string {
    const parts: string[] = [];
    if (p.kind === 'artist') {
      parts.push((p.allowedSources ?? []).join(', '));
    } else {
      const res = [...(p.allowedRes ?? [])].sort((a, b) => a - b).map((r) => `${r}p`);
      if (p.preferredRes) parts.push(t('quality.preferred', { res: `${p.preferredRes}p` }));
      if (res.length > 0) parts.push(res.join(', '));
      const sources = p.allowedSources ?? [];
      parts.push(sources.length > 0 ? sources.join(', ') : t('quality.anySource'));
      if (p.preferHdr) parts.push(t('quality.hdr'));
    }
    if (p.maxSizeMb) parts.push(t('quality.upTo', { size: gb(p.maxSizeMb) }));
    return parts.filter(Boolean).join(' · ');
  }

  // ---- edit ----
  let editing = $state<Profile | undefined>();
  let formOpen = $state(false);
  let name = $state('');
  let res = $state<Record<number, boolean>>({});
  let preferred = $state('0');
  let sources = $state<Record<string, boolean>>({});
  let minGb = $state('');
  let maxGb = $state('');
  let perHourGb = $state('');
  let hdr = $state(false);
  let preferredWords = $state('');
  let rejectedWords = $state('');
  let seeders = $state('0');
  let upgradeUntil = $state('');
  let busy = $state(false);
  let formError = $state('');
  let nameError = $state('');
  let sizeError = $state('');
  let seedersError = $state('');

  const isMusic = $derived(editing?.kind === 'artist');
  const isShow = $derived(editing?.kind === 'show');
  const sourceChoices = $derived<readonly string[]>(isMusic ? MUSIC_FORMATS : VIDEO_SOURCES);
  const allowedNow = $derived(RESOLUTIONS.filter((r) => res[r]));

  const mbToGb = (mb: number | null): string => (mb === null || mb === 0 ? '' : String(Math.round((mb / 1024) * 10) / 10));

  function openEdit(p: Profile): void {
    editing = p;
    name = p.name;
    res = Object.fromEntries(RESOLUTIONS.map((r) => [r, (p.allowedRes ?? []).includes(r)]));
    preferred = String(p.preferredRes || 0);
    sources = Object.fromEntries(
      (p.kind === 'artist' ? MUSIC_FORMATS : VIDEO_SOURCES).map((s) => [s, (p.allowedSources ?? []).includes(s)]),
    );
    minGb = mbToGb(p.minSizeMb);
    maxGb = mbToGb(p.maxSizeMb);
    perHourGb = mbToGb(p.maxSizePerHourMb);
    hdr = p.preferHdr;
    preferredWords = (p.preferredWords ?? []).join(', ');
    rejectedWords = (p.rejectedWords ?? []).join(', ');
    seeders = String(p.minSeeders);
    upgradeUntil = p.upgradeUntil ?? '';
    formError = nameError = sizeError = seedersError = '';
    formOpen = true;
  }

  /** "" is no limit (null); otherwise gigabytes with an optional decimal part, as megabytes. */
  function parseGb(text: string): number | null | undefined {
    const trimmed = text.trim().replace(',', '.');
    if (trimmed === '') return null;
    if (!/^\d{1,6}(\.\d{1,2})?$/.test(trimmed)) return undefined;
    return Math.round(Number(trimmed) * 1024);
  }

  const words = (text: string): string[] =>
    text
      .split(',')
      .map((w) => w.trim())
      .filter(Boolean);

  async function save(): Promise<void> {
    const p = editing;
    if (!p || busy) return;
    nameError = sizeError = seedersError = '';
    const min = parseGb(minGb);
    const max = parseGb(maxGb);
    const perHour = parseGb(perHourGb);
    if (!name.trim()) nameError = t('quality.error.name');
    if (min === undefined || max === undefined || perHour === undefined) sizeError = t('quality.error.size');
    if (!/^\d{1,4}$/.test(seeders.trim())) seedersError = t('quality.error.seeders');
    if (nameError || sizeError || seedersError) return;

    const chosenSources = sourceChoices.filter((s) => sources[s]);
    busy = true;
    formError = '';
    try {
      const saved = await updateProfile(p.id, {
        name: name.trim(),
        allowedRes: isMusic ? [] : [...allowedNow],
        preferredRes: isMusic ? 0 : Number(preferred),
        allowedSources: chosenSources,
        minSizeMb: min ?? null,
        maxSizeMb: max ?? null,
        maxSizePerHourMb: isShow ? (perHour ?? null) : null,
        preferHdr: isMusic ? false : hdr,
        preferredWords: words(preferredWords),
        rejectedWords: words(rejectedWords),
        minSeeders: Number(seeders),
        upgradeUntil: upgradeUntil.trim() || null,
      });
      profiles = profiles.map((x) => (x.id === saved.id ? saved : x));
      formOpen = false;
      onnote?.(t('quality.saved', { name: labelOf(saved) }));
    } catch (err) {
      formError = failureText(err);
    } finally {
      busy = false;
    }
  }

  const preferredOptions = $derived([
    { value: '0', label: t('quality.noPreference') },
    ...allowedNow.map((r) => ({ value: String(r), label: `${r}p` })),
  ]);
  // The preferred resolution has to be one of the allowed ones.
  $effect(() => {
    if (preferred !== '0' && !allowedNow.includes(Number(preferred) as (typeof RESOLUTIONS)[number])) {
      preferred = '0';
    }
  });
</script>

<div class="grid">
  <Panel labelledby={listId} padding="18px">
    <SectionHead title={t('quality.heading')} id={listId} size="md" align="center" gap={4} minHeight={36} />
    <p class="help">{t('quality.help')}</p>
    {#if failed && !loaded}
      <LoadFailed onretry={() => void load()} />
    {:else if !loaded}
      <div class="loading" role="status" aria-busy="true">
        <span class="sr-only">{t('load.loading')}</span>
        <Skeleton height="64px" radius="var(--radius-md)" />
        <Skeleton height="64px" radius="var(--radius-md)" />
      </div>
    {:else if profiles.length === 0}
      <p class="help">{t('quality.none')}</p>
    {:else}
      {#each groups as group (group.kind)}
        <h3 class="group">{kindHeading(group.kind)}</h3>
        <ul>
          {#each group.items as profile (profile.id)}
            <li>
              <div class="text">
                <div class="name-row">
                  <span class="name">{profile.name}</span>
                  {#if profile.kind !== 'artist' && profile.name !== qualityWord(profile)}
                    <Chip tone="faint" size="micro">{qualityWord(profile)}</Chip>
                  {/if}
                </div>
                <div class="summary">{summary(profile)}</div>
              </div>
              <Button
                size="compact"
                aria-label={t('settings.rowAria', { action: t('settings.action.edit'), name: labelOf(profile) })}
                onclick={() => openEdit(profile)}
              >
                {t('settings.action.edit')}
              </Button>
            </li>
          {/each}
        </ul>
      {/each}
    {/if}
  </Panel>

  <Panel labelledby={blockId} padding="18px">
    <SectionHead title={t('quality.blocklist.heading')} id={blockId} size="md" align="center" gap={4} minHeight={36} />
    <p class="help">{t('quality.blocklist.help')}</p>
    {#if !blockLoaded}
      <div class="loading" role="status" aria-busy="true">
        <span class="sr-only">{t('load.loading')}</span>
        <Skeleton height="48px" radius="var(--radius-md)" />
      </div>
    {:else if blocked.length === 0}
      <p class="help">{t('quality.blocklist.none')}</p>
    {:else}
      <ul>
        {#each blocked as entry (entry.id)}
          <li>
            <div class="text">
              <div class="name">{entry.releaseTitle}</div>
              <div class="summary">
                {entry.reason ? `${entry.reason} · ` : ''}{formatDay(entry.createdAt)}
              </div>
            </div>
            <Button
              size="compact"
              disabled={unblocking === entry.id}
              aria-label={t('quality.unblockAria', { name: entry.releaseTitle })}
              onclick={() => unblock(entry)}
            >
              {t('quality.unblock')}
            </Button>
          </li>
        {/each}
      </ul>
      {#if blockedTotal > blocked.length}
        <p class="help">{t('quality.blocklist.more', { n: blockedTotal - blocked.length })}</p>
      {/if}
    {/if}
  </Panel>
</div>

<FormSheet
  bind:open={formOpen}
  title={t('quality.edit.title', { name: editing ? labelOf(editing) : '' })}
  intro={t('quality.edit.intro')}
  submitLabel={t('apps.save')}
  {busy}
  error={formError}
  onsubmit={save}
  wide
>
  <Field
    label={t('apps.field.name')}
    bind:value={name}
    name="name"
    error={nameError || undefined}
    oninput={() => (nameError = '')}
  />

  {#if !isMusic}
    <fieldset class="checks">
      <legend>{t('quality.field.resolutions')}</legend>
      <div class="check-grid">
        {#each RESOLUTIONS as r (r)}
          <CheckField label={`${r}p`} checked={res[r] === true} onchange={(on) => (res[r] = on)} />
        {/each}
      </div>
    </fieldset>
    <SelectField label={t('quality.field.preferred')} bind:value={preferred} options={preferredOptions} />
  {/if}

  <fieldset class="checks">
    <legend>{isMusic ? t('quality.field.formats') : t('quality.field.sources')}</legend>
    <p class="legend-help">{isMusic ? t('quality.field.formatsHelp') : t('quality.field.sourcesHelp')}</p>
    <div class="check-grid">
      {#each sourceChoices as s (s)}
        <CheckField label={s} checked={sources[s] === true} onchange={(on) => (sources[s] = on)} />
      {/each}
    </div>
  </fieldset>

  <Field label={t('quality.field.min')} bind:value={minGb} inputmode="decimal" name="min" help={t('quality.field.gbHelp')} />
  <Field
    label={t('quality.field.max')}
    bind:value={maxGb}
    inputmode="decimal"
    name="max"
    help={t('quality.field.gbHelp')}
    error={sizeError || undefined}
    oninput={() => (sizeError = '')}
  />
  {#if isShow}
    <Field
      label={t('quality.field.perHour')}
      bind:value={perHourGb}
      inputmode="decimal"
      name="perHour"
      help={t('quality.field.perHourHelp')}
    />
  {/if}
  {#if !isMusic}
    <CheckField label={t('quality.field.hdr')} bind:checked={hdr} />
  {/if}
  <Field
    label={t('quality.field.preferredWords')}
    bind:value={preferredWords}
    name="preferredWords"
    help={t('quality.field.wordsHelp')}
  />
  <Field
    label={t('quality.field.rejectedWords')}
    bind:value={rejectedWords}
    name="rejectedWords"
    help={t('quality.field.wordsHelp')}
  />
  <Field
    label={t('quality.field.seeders')}
    bind:value={seeders}
    inputmode="numeric"
    name="seeders"
    help={t('quality.field.seedersHelp')}
    error={seedersError || undefined}
    oninput={() => (seedersError = '')}
  />
  <Field
    label={t('quality.field.upgrade')}
    bind:value={upgradeUntil}
    name="upgradeUntil"
    placeholder="Bluray-1080p"
    help={t('quality.field.upgradeHelp')}
  />
</FormSheet>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
    align-items: start;
    gap: 16px;
  }
  .help {
    margin-top: 8px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 12px;
  }
  .group {
    margin: 16px 0 2px;
    font-size: 11px;
    font-weight: 600;
    line-height: 14px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--muted);
  }
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
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
  .summary {
    margin-top: 3px;
    font-size: 13px;
    line-height: 18px;
    color: var(--text-3);
    overflow-wrap: anywhere;
  }
  .checks {
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }
  .checks legend {
    padding: 0;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--text-2);
  }
  .legend-help {
    margin: 4px 0 0;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .check-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
    gap: 10px 14px;
    margin-top: 10px;
  }
</style>
