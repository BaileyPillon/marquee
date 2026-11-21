<script lang="ts">
  import Icon from './Icon.svelte';
  import { t } from '../core/i18n';

  interface Props {
    /** rail: 28px mark and 27px serif. phone: 24px mark and 26px serif. */
    size?: 'rail' | 'phone';
  }

  let { size = 'rail' }: Props = $props();
</script>

<a class="wordmark {size}" href="/">
  <Icon name="logo" size={size === 'rail' ? 28 : 24} stroke={1.6} />
  <span>{t('app.name')}</span>
</a>

<style>
  .wordmark {
    display: flex;
    align-items: center;
    color: var(--text);
    text-decoration: none;
  }
  .wordmark:hover {
    color: var(--text);
  }
  span {
    font-family: var(--font-serif);
    font-weight: 400;
    line-height: 1;
    letter-spacing: 0.01em;
  }
  .rail {
    gap: 10px;
    padding: 0 10px 24px;
  }
  .rail span {
    font-size: 27px;
  }
  .phone {
    gap: 8px;
  }
  .phone span {
    font-size: 26px;
  }
</style>
