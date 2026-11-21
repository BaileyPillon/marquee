<script lang="ts">
  import Button from '../lib/ui/Button.svelte';
  import PageHeader from '../lib/ui/PageHeader.svelte';
  import { t } from '../lib/core/i18n';
</script>

<PageHeader title={t('notFound.title')} subtitle={t('notFound.body')} />
<Button href="/" variant="primary">{t('notFound.home')}</Button>
