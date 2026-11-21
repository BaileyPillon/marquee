<script lang="ts">
  interface Props {
    label: string;
    checked: boolean;
    /** Quiet hint under the label. */
    help?: string;
    disabled?: boolean;
    onchange?: (checked: boolean) => void;
  }

  let { label, checked = $bindable(), help, disabled = false, onchange }: Props = $props();

  const uid = $props.id();
  const helpId = `check-${uid}-help`;
</script>

<label class="check" class:disabled>
  <input
    type="checkbox"
    bind:checked
    {disabled}
    aria-describedby={help ? helpId : undefined}
    onchange={() => onchange?.(checked)}
  />
  <span class="text">
    <span class="label">{label}</span>
    {#if help}<span id={helpId} class="help">{help}</span>{/if}
  </span>
</label>

<style>
  .check {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    cursor: pointer;
  }
  .disabled {
    cursor: default;
    opacity: 0.6;
  }
  input {
    flex: 0 0 auto;
    width: 20px;
    height: 20px;
    margin: 1px 0 0;
    accent-color: var(--accent);
    color-scheme: dark;
  }
  .text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .label {
    font-size: 14.5px;
    font-weight: 500;
    line-height: 22px;
    color: var(--text);
  }
  .help {
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
</style>
