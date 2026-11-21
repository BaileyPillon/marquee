<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import SectionHead from '../../lib/ui/SectionHead.svelte';
  import { t } from '../../lib/core/i18n';
  import type { McpKey } from '../../lib/core/views';

  interface Props {
    keys: McpKey[];
  }

  let { keys }: Props = $props();

  const uid = $props.id();
  const headId = `keys-${uid}`;
</script>

<Panel labelledby={headId} padding="18px">
  <SectionHead title={t('settings.panel.mcp')} id={headId} size="md" align="center" gap={4} minHeight={36}>
    <Button variant="primary" size="sm">
      <Icon name="plus" size={16} stroke={2.2} />
      {t('settings.newKey')}
    </Button>
  </SectionHead>
  <ul>
    {#each keys as key (key.name)}
      <li>
        <div class="line">
          <div class="text">
            <div class="name">{key.name}</div>
            <div class="status">{key.status}</div>
          </div>
          <Button
            size="compact"
            aria-label={t('settings.rowAria', { action: t('settings.action.revoke'), name: key.name })}
          >
            {t('settings.action.revoke')}
          </Button>
        </div>
        <ul class="abilities" aria-label={t('settings.abilities')}>
          {#each key.abilities as ability (ability.id)}
            <li>
              <Chip tone={ability.allowed ? 'allowed' : 'denied'}>
                {#snippet icon()}
                  <Icon name={ability.allowed ? 'check' : 'close'} size={14} stroke={2.4} />
                {/snippet}
                <span>{t(`settings.ability.${ability.id}`)}</span>
                <span class="sr-only">
                  {ability.allowed ? t('settings.allowed') : t('settings.notAllowed')}
                </span>
              </Chip>
            </li>
          {/each}
        </ul>
      </li>
    {/each}
  </ul>
  <p class="note">{t('settings.note.mcp')}</p>
</Panel>

<style>
  .line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 10px 14px;
  }
  .line :global(.btn) {
    padding: 0 16px;
  }
  .text {
    flex: 1 1 220px;
    min-width: 0;
  }
  .name {
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .status {
    margin-top: 3px;
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--text-3);
  }
  .abilities {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 12px;
  }
  .abilities li {
    position: relative;
  }
  .note {
    margin-top: 12px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  ul > li {
    padding: 14px 0;
  }
  .abilities > li {
    padding: 0;
  }
</style>
