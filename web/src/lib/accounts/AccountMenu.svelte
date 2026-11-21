<script lang="ts">
  import Icon from '../ui/Icon.svelte';
  import { t } from '../core/i18n';
  import { session, type Role } from '../core/session.svelte';

  interface Props {
    owner: { name: string; initial: string; role: Role };
    /** rail: the name and role beside the avatar, the menu opens upward. phone: just the avatar. */
    variant: 'rail' | 'phone';
  }

  let { owner, variant }: Props = $props();

  const uid = $props.id();
  const panelId = `account-menu-${uid}`;
  let open = $state(false);
  let signingOut = $state(false);
  let failed = $state(false);
  let root: HTMLDivElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();

  function close(refocus = false): void {
    open = false;
    failed = false;
    if (refocus) trigger?.focus();
  }

  function onkeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) close(true);
  }

  // A press anywhere else closes it, and so does tabbing out of it.
  function onpointerdown(event: PointerEvent): void {
    if (open && root && event.target instanceof Node && !root.contains(event.target)) close();
  }
  function onfocusout(event: FocusEvent): void {
    if (open && root && event.relatedTarget instanceof Node && !root.contains(event.relatedTarget)) {
      close();
    }
  }

  async function signOut(): Promise<void> {
    if (signingOut) return;
    signingOut = true;
    failed = false;
    const ok = await session.signOut();
    // On success the whole shell goes away; only a failure is left to report.
    if (!ok) {
      failed = true;
      signingOut = false;
    }
  }
</script>

<svelte:window {onkeydown} {onpointerdown} />

<div class="menu {variant}" bind:this={root} {onfocusout}>
  <button
    type="button"
    class="trigger"
    bind:this={trigger}
    aria-expanded={open}
    aria-controls={panelId}
    aria-label={variant === 'phone' ? t('menu.account', { name: owner.name }) : undefined}
    onclick={() => (open ? close() : (open = true))}
  >
    <span class="avatar" aria-hidden="true">{owner.initial}</span>
    {#if variant === 'rail'}
      <span class="who">{owner.name}<span class="role">{` · ${t(`role.${owner.role}`)}`}</span></span>
    {/if}
  </button>

  <div id={panelId} class="panel" role="group" aria-label={t('menu.label')} hidden={!open}>
    <div class="head">
      <span class="head-name">{owner.name}</span>
      <span class="role">{t(`role.${owner.role}`)}</span>
    </div>
    <a class="item" href="/settings" onclick={() => close()}>
      <Icon name="settings" size={18} />
      {t('menu.settings')}
    </a>
    <button type="button" class="item" aria-disabled={signingOut ? 'true' : undefined} onclick={signOut}>
      <Icon name="signOut" size={18} />
      {signingOut ? t('menu.signingOut') : t('menu.signOut')}
    </button>
    {#if failed}
      <p class="fail" role="alert">{t('menu.signOutFailed')}</p>
    {/if}
  </div>
</div>

<style>
  .menu {
    position: relative;
  }
  .trigger {
    display: flex;
    align-items: center;
    gap: 10px;
    box-sizing: border-box;
    margin: 0;
    border: 0;
    background: transparent;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }
  .trigger:hover {
    background: var(--hover);
  }

  .rail .trigger {
    width: 100%;
    padding: 8px 10px;
    border-radius: var(--radius-sm);
  }
  .phone .trigger {
    justify-content: center;
    width: 44px;
    height: 44px;
    padding: 0;
    border-radius: 22px;
  }

  .avatar {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: var(--avatar);
    font-size: 14px;
    font-weight: 600;
    line-height: 1;
    color: var(--text);
  }
  .who {
    min-width: 0;
    font-size: 14px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .role {
    color: var(--muted);
  }

  .panel {
    position: absolute;
    z-index: 30;
    box-sizing: border-box;
    padding: 6px;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius-md);
    background: var(--panel);
    box-shadow: 0 10px 28px rgba(0, 0, 0, 0.5);
  }
  .panel[hidden] {
    display: none;
  }
  .rail .panel {
    left: 0;
    right: 0;
    bottom: calc(100% + 8px);
  }
  .phone .panel {
    right: 0;
    top: calc(100% + 4px);
    min-width: 220px;
  }

  .head {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 8px 10px 10px;
    margin-bottom: 4px;
    border-bottom: 1px solid var(--hairline);
    font-size: 14px;
    line-height: 18px;
  }
  .head-name {
    font-weight: 600;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .head .role {
    font-size: 13px;
  }

  .item {
    display: flex;
    align-items: center;
    gap: 12px;
    box-sizing: border-box;
    width: 100%;
    min-height: 44px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text-2);
    font-family: var(--font-sans);
    font-size: 14.5px;
    font-weight: 500;
    line-height: 1;
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }
  .item:hover {
    background: var(--hover);
    color: var(--text);
  }
  .item[aria-disabled='true'] {
    cursor: progress;
    opacity: 0.7;
  }
  .fail {
    margin: 4px 10px 6px;
    font-size: 13px;
    line-height: 18px;
    color: var(--danger);
  }
</style>
