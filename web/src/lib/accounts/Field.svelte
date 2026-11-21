<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements';
  import ErrorNote from './ErrorNote.svelte';

  interface Props {
    label: string;
    value: string;
    type?: 'text' | 'password';
    name?: string;
    autocomplete?: HTMLInputAttributes['autocomplete'];
    required?: boolean;
    /** Quiet hint under the field; stays visible next to an error. */
    help?: string;
    /** Error text; sets aria-invalid and is read out as an alert. */
    error?: string;
    onblur?: () => void;
    oninput?: () => void;
  }

  let {
    label,
    value = $bindable(),
    type = 'text',
    name,
    autocomplete,
    required = false,
    help,
    error,
    onblur,
    oninput,
  }: Props = $props();

  const uid = $props.id();
  const id = `field-${uid}`;
  const helpId = `${id}-help`;
  const errorId = `${id}-error`;
  let input: HTMLInputElement | undefined = $state();

  const describedby = $derived([help ? helpId : '', error ? errorId : ''].join(' ').trim() || undefined);

  /** Move the cursor into the field (used to land on the first mistake). */
  export function focus(options?: FocusOptions): void {
    input?.focus(options);
  }
</script>

<div class="field">
  <label for={id}>{label}</label>
  <input
    {id}
    {name}
    {type}
    {autocomplete}
    {required}
    {onblur}
    {oninput}
    bind:value
    bind:this={input}
    class:invalid={!!error}
    aria-invalid={error ? 'true' : undefined}
    aria-describedby={describedby}
    autocapitalize="off"
    spellcheck="false"
  />
  {#if help}<p id={helpId} class="help">{help}</p>{/if}
  {#if error}
    <div class="error"><ErrorNote id={errorId}>{error}</ErrorNote></div>
  {/if}
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
  input {
    box-sizing: border-box;
    width: 100%;
    height: 48px;
    padding: 0 14px;
    background: var(--input);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 15px;
    font-weight: 400;
    line-height: 1;
    color-scheme: dark;
  }
  input:focus-visible {
    border-color: var(--accent);
  }
  /* A mistake: coral outline, doubled with an inset ring so it does not rely on colour alone. */
  input.invalid {
    border-color: var(--danger);
    box-shadow: inset 0 0 0 1px var(--danger);
  }
  .help {
    margin: 6px 0 0;
    font-size: 13px;
    font-weight: 400;
    line-height: 18px;
    color: var(--muted);
  }
  .error {
    margin-top: 8px;
  }
</style>
