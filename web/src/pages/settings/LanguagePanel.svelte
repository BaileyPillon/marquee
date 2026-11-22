<script lang="ts">
  import LanguagePicker from '../../lib/ui/LanguagePicker.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import { t } from '../../lib/core/i18n';

  const uid = $props.id();
  const headId = `language-${uid}`;
</script>

<!-- Settings › Language: every language Marquee speaks, each in its own name. The same list opens from the account menu. -->
<Panel labelledby={headId} padding="18px">
  <SectionHead title={t('lang.heading')} id={headId} size="md" align="center" gap={4} />
  <p class="help">{t('lang.help')}</p>
  <div class="picker"><LanguagePicker grid /></div>
</Panel>

<style>
  .help {
    margin-top: 8px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  .picker {
    margin-top: 16px;
  }
</style>
