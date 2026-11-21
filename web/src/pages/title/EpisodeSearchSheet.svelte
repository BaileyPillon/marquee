<script lang="ts">
  import BottomSheet from '../../lib/ui/BottomSheet.svelte';
  import Button from '../../lib/ui/Button.svelte';
  import ReleasesPanel from '../../lib/ui/ReleasesPanel.svelte';
  import { t } from '../../lib/core/i18n';

  interface Props {
    /** Whether the dialog is showing. Bind it. */
    open: boolean;
    titleId: number;
    name: string;
    season: number;
    episode: number;
    /** A grab went through: the page may refresh what it shows. */
    ongrabbed?: () => void;
  }

  let { open = $bindable(false), titleId, name, season, episode, ongrabbed }: Props = $props();

  const code = $derived(
    `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`,
  );
</script>

<!-- One episode: the sources are asked as soon as the dialog opens, because the owner just pressed Search. -->
<BottomSheet bind:open title={t('show.search.title', { name, code })} wide>
  <div class="body">
    <ReleasesPanel {titleId} {name} kind="show" {season} {episode} compact autorun {ongrabbed} />
  </div>
  <div class="close">
    <Button variant="ghost" size="lg" block onclick={() => (open = false)}>{t('ask.close')}</Button>
  </div>
</BottomSheet>

<style>
  .body {
    margin-top: 14px;
  }
  .close {
    margin-top: 12px;
  }
</style>
