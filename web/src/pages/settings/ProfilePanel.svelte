<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LabelValue from '../../lib/ui/LabelValue.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import { t } from '../../lib/core/i18n';
  import { session } from '../../lib/core/session.svelte';

  const uid = $props.id();
  const headId = `profile-${uid}`;
  let busy = $state(false);
  let failed = $state(false);

  async function signOut(): Promise<void> {
    if (busy) return;
    busy = true;
    failed = false;
    const ok = await session.signOut();
    if (!ok) {
      failed = true;
      busy = false;
    }
  }
</script>

<!-- A member has no server settings: their own page is who they are and how to leave. -->
<Panel labelledby={headId} padding="18px">
  <SectionHead title={t('profile.heading')} id={headId} size="md" align="center" gap={4} />
  {#if session.me}
    <LabelValue
      items={[
        { label: t('profile.name'), value: session.me.name },
        { label: t('profile.role'), value: t(`role.${session.me.role}`) },
      ]}
    />
  {/if}
  <p class="note">{t('profile.note')}</p>
  <div class="actions">
    <Button onclick={signOut} disabled={busy}>
      <Icon name="signOut" size={18} />
      {busy ? t('menu.signingOut') : t('menu.signOut')}
    </Button>
  </div>
  {#if failed}<p class="failed" role="alert">{t('menu.signOutFailed')}</p>{/if}
</Panel>

<style>
  .note {
    margin-top: 14px;
    font-size: 13.5px;
    line-height: 19px;
    color: var(--text-3);
  }
  .actions {
    margin-top: 16px;
  }
  .failed {
    margin-top: 10px;
    font-size: 13.5px;
    color: var(--danger);
  }
</style>
