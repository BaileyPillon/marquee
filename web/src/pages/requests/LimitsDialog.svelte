<script lang="ts">
  import Field from '../../lib/accounts/Field.svelte';
  import FormSheet from '../../lib/ui/FormSheet.svelte';
  import SelectField from '../../lib/ui/SelectField.svelte';
  import { failureText } from '../../lib/core/catalog';
  import { t } from '../../lib/core/i18n';
  import { peopleState } from '../../lib/core/people.svelte';
  import { setLimits, type PersonInfo } from '../../lib/core/requestsApi';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    /** The person who is chosen when the dialog opens. */
    initialId?: number;
    /** A save went through: the person as the server now has them. */
    onsaved: (person: PersonInfo) => void;
  }

  let { open = $bindable(false), initialId, onsaved }: Props = $props();

  type Flag = 'default' | 'on' | 'off';

  const people = $derived(peopleState.people);
  const defaults = $derived(peopleState.defaults);

  let personId = $state('');
  let hd = $state('');
  let uhd = $state('');
  let autoHd = $state<Flag>('default');
  let autoUhd = $state<Flag>('default');
  let busy = $state(false);
  let error = $state('');
  let hdError = $state('');
  let uhdError = $state('');

  const person = $derived(people.find((p) => String(p.id) === personId));

  function flagOf(value: boolean | null): Flag {
    return value === null ? 'default' : value ? 'on' : 'off';
  }

  // Fill the form from the chosen person, each time the dialog opens or the person changes.
  $effect(() => {
    if (!open) return;
    if (personId === '' || !people.some((p) => String(p.id) === personId)) {
      const first = people.find((p) => p.id === initialId) ?? people[0];
      personId = first ? String(first.id) : '';
    }
  });
  $effect(() => {
    const p = person;
    if (!p) return;
    const o = p.limits.overrides;
    hd = o.hdPerWeek === null ? '' : String(o.hdPerWeek);
    uhd = o.uhdPerWeek === null ? '' : String(o.uhdPerWeek);
    autoHd = flagOf(o.autoApproveHD);
    autoUhd = flagOf(o.autoApproveUHD);
    error = hdError = uhdError = '';
  });

  function flagOptions(def: boolean | undefined): Array<{ value: string; label: string }> {
    return [
      {
        value: 'default',
        label:
          def === undefined
            ? t('limits.flag.default')
            : t('limits.flag.defaultValue', { value: def ? t('toggle.on') : t('toggle.off') }),
      },
      { value: 'on', label: t('toggle.on') },
      { value: 'off', label: t('toggle.off') },
    ];
  }

  /** "" is the default (null); otherwise a whole number from 0 up. */
  function parseLimit(text: string): number | null | undefined {
    const trimmed = text.trim();
    if (trimmed === '') return null;
    if (!/^\d{1,5}$/.test(trimmed)) return undefined;
    return Number(trimmed);
  }

  const toFlag = (f: Flag): boolean | null => (f === 'default' ? null : f === 'on');

  async function save(): Promise<void> {
    if (!person || busy) return;
    const hdValue = parseLimit(hd);
    const uhdValue = parseLimit(uhd);
    hdError = hdValue === undefined ? t('limits.error.number') : '';
    uhdError = uhdValue === undefined ? t('limits.error.number') : '';
    if (hdValue === undefined || uhdValue === undefined) return;
    busy = true;
    error = '';
    try {
      const saved = await setLimits(person.id, {
        hdPerWeek: hdValue,
        uhdPerWeek: uhdValue,
        autoApproveHD: toFlag(autoHd),
        autoApproveUHD: toFlag(autoUhd),
      });
      peopleState.replace(saved);
      open = false;
      onsaved(saved);
    } catch (err) {
      error = failureText(err);
    } finally {
      busy = false;
    }
  }
</script>

<FormSheet
  bind:open
  title={t('limits.title')}
  intro={t('limits.intro')}
  submitLabel={t('limits.save')}
  {busy}
  {error}
  onsubmit={save}
>
  <SelectField
    label={t('limits.person')}
    bind:value={personId}
    options={people.map((p) => ({ value: String(p.id), label: p.name }))}
  />
  <Field
    label={t('limits.hd')}
    bind:value={hd}
    inputmode="numeric"
    name="hd"
    placeholder={defaults ? t('limits.placeholder', { n: defaults.hdPerWeek }) : ''}
    help={t('limits.help')}
    error={hdError || undefined}
  />
  <Field
    label={t('limits.uhd')}
    bind:value={uhd}
    inputmode="numeric"
    name="uhd"
    placeholder={defaults ? t('limits.placeholder', { n: defaults.uhdPerWeek }) : ''}
    error={uhdError || undefined}
  />
  <SelectField
    label={t('limits.autoHd')}
    bind:value={autoHd}
    options={flagOptions(defaults?.autoApproveHD)}
  />
  <SelectField
    label={t('limits.autoUhd')}
    bind:value={autoUhd}
    options={flagOptions(defaults?.autoApproveUHD)}
  />
</FormSheet>
