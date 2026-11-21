<script lang="ts">
  interface Option {
    value: string;
    label: string;
  }

  interface Props {
    label: string;
    value: string;
    options: Option[];
    /** Quiet hint under the field. */
    help?: string;
    disabled?: boolean;
    onchange?: () => void;
  }

  let { label, value = $bindable(), options, help, disabled = false, onchange }: Props = $props();

  const uid = $props.id();
  const id = `select-${uid}`;
  const helpId = `${id}-help`;
</script>

<div class="field">
  <label for={id}>{label}</label>
  <select {id} bind:value {disabled} {onchange} aria-describedby={help ? helpId : undefined}>
    {#each options as option (option.value)}
      <option value={option.value}>{option.label}</option>
    {/each}
  </select>
  {#if help}<p id={helpId} class="help">{help}</p>{/if}
</div>

<style>
  label {
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
  select:focus-visible {
    border-color: var(--accent);
  }
  .help {
    margin: 6px 0 0;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
</style>
