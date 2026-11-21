<script lang="ts">
  import Chip from './Chip.svelte';
  import { t } from '../core/i18n';
</script>

<!-- A section that still shows sample content: its backend is not built yet. -->
<Chip tone="faint" size="micro"
  >{t('preview.tag')}<span class="sr-only">{t('preview.sr')}</span></Chip
>
