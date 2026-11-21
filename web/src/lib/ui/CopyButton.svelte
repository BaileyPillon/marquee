<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** The text that goes on the clipboard. */
    text: string;
    /** What it is, for the button's name: "Copy the address". */
    label: string;
    size?: 'xs' | 'compact' | 'sm' | 'md';
  }

  let { text, label, size = 'compact' }: Props = $props();

  let done = $state(false);
  let failed = $state(false);
  let timer: number | undefined;

  async function copy(): Promise<void> {
    window.clearTimeout(timer);
    failed = false;
    try {
      await navigator.clipboard.writeText(text);
      done = true;
    } catch {
      done = false;
      failed = true;
    }
    timer = window.setTimeout(() => {
      done = false;
      failed = false;
    }, 2500);
  }

  $effect(() => () => window.clearTimeout(timer));
</script>

<Button {size} aria-label={label} onclick={copy}>
  <Icon name={done ? 'check' : 'copy'} size={16} stroke={2.2} />
  {done ? t('copy.done') : t('copy.action')}
</Button>
<span class="sr-only" role="status" aria-live="polite">
  {done ? t('copy.doneSr') : failed ? t('copy.failed') : ''}
</span>
