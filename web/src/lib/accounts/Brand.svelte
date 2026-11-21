<script lang="ts">
  import Icon from '../ui/Icon.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** lg: 30px mark and 32px serif (desktop). sm: 28px mark and 30px serif (phone sign-in). */
    size?: 'lg' | 'sm';
  }

  let { size = 'lg' }: Props = $props();
</script>

<div class="brand {size}">
  <Icon name="logo" size={size === 'lg' ? 30 : 28} stroke={1.6} />
  <span>{t('app.name')}</span>
</div>

<style>
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--text);
  }
  span {
    font-family: var(--font-serif);
    letter-spacing: 0.01em;
    line-height: 1;
  }
  .lg span {
    font-size: 32px;
  }
  .sm span {
    font-size: 30px;
    line-height: 30px;
  }
</style>
