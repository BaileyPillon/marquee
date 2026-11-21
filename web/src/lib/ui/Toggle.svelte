<script lang="ts">
  import { t } from '../core/i18n';

  interface Props {
    checked: boolean;
    /** What the switch does, e.g. "Watch for new episodes". */
    label: string;
    onchange: (checked: boolean) => void;
  }

  let { checked, label, onchange }: Props = $props();
</script>

<button type="button" class="toggle" role="switch" aria-checked={checked} onclick={() => onchange(!checked)}>
  <span>{label}</span>
  <span class="state">{checked ? t('toggle.on') : t('toggle.off')}</span>
  <span class="track" class:on={checked} aria-hidden="true"><span class="knob"></span></span>
</button>

<style>
  .toggle {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    box-sizing: border-box;
    height: 44px;
    padding: 0 14px 0 18px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text);
    font-size: 14px;
    font-weight: 600;
    line-height: 1;
    cursor: pointer;
  }
  .toggle:hover {
    background: var(--hover);
  }
  .state {
    font-weight: 700;
  }
  .track {
    position: relative;
    flex: 0 0 auto;
    display: inline-block;
    width: 36px;
    height: 20px;
    border-radius: 10px;
    background: var(--track);
    transition: background-color 0.12s ease;
  }
  .track.on {
    background: var(--accent);
  }
  .knob {
    position: absolute;
    top: 3px;
    left: 3px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--text);
    transition: transform 0.12s ease;
  }
  .on .knob {
    background: var(--on-accent);
    transform: translateX(16px);
  }
</style>
