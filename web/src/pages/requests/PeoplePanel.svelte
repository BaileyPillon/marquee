<script lang="ts">
  import { onMount } from 'svelte';
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LabelValue from '../../lib/ui/LabelValue.svelte';
  import NotSetUp from '../../lib/ui/NotSetUp.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import Skeleton from '../../lib/ui/Skeleton.svelte';
  import { debounce, onEvent, onResync } from '../../lib/core/events';
  import { t } from '../../lib/core/i18n';
  import { peopleState } from '../../lib/core/people.svelte';
  import type { PersonInfo } from '../../lib/core/requestsApi';
  import type { Usage } from '../../lib/core/catalog';
  import LimitsDialog from './LimitsDialog.svelte';

  interface Props {
    /** The owner changes limits; an admin only looks. */
    canEdit: boolean;
    /** A save went through. */
    onsaved?: (person: PersonInfo) => void;
  }

  let { canEdit, onsaved }: Props = $props();

  const uid = $props.id();
  const peopleId = `people-${uid}`;

  let limitsOpen = $state(false);

  onMount(() => {
    void peopleState.reload();
    // Use moves as asks are made and answered.
    const soon = debounce(() => void peopleState.reload(), 400);
    const stop = onEvent('requests', () => soon.call());
    const stopResync = onResync(() => void peopleState.reload());
    return () => {
      soon.cancel();
      stop();
      stopResync();
    };
  });

  function usage(u: Usage): string {
    return u.limit === null
      ? t('requests.usage.noLimit', { used: u.used })
      : t('requests.usage.of', { used: u.used, limit: u.limit });
  }

  const people = $derived(peopleState.people);
</script>

<Panel labelledby={peopleId} padding="20px">
  <h2 id={peopleId} class="h2">{t('requests.people')}</h2>
  <p class="note">{t('requests.peopleNote')}</p>
  {#if peopleState.status === 'loading' || peopleState.status === 'idle'}
    <div class="loading" role="status" aria-busy="true">
      <span class="sr-only">{t('load.loading')}</span>
      <Skeleton height="56px" radius="var(--radius-md)" />
      <Skeleton height="56px" radius="var(--radius-md)" />
    </div>
  {:else if peopleState.status === 'unavailable'}
    <NotSetUp />
  {:else if peopleState.status === 'failed'}
    <p class="failed" role="alert">{t('load.failed')}</p>
    <div class="foot">
      <Button size="sm" onclick={() => void peopleState.reload()}>{t('load.retry')}</Button>
    </div>
  {:else if people.length === 0}
    <p class="empty">{t('requests.noPeople')}</p>
  {:else}
    <ul class="people">
      {#each people as person (person.id)}
        <li>
          <span class="avatar" aria-hidden="true">{[...person.name][0]?.toUpperCase() ?? '?'}</span>
          <div class="person">
            <div class="name">
              {person.name}
              {#if person.role !== 'member'}<span class="role">{t(`role.${person.role}`)}</span>{/if}
            </div>
            <LabelValue
              variant="plain"
              items={[
                { label: t('requests.hdAsks'), value: usage(person.usage.hd) },
                { label: t('requests.fourKAsks'), value: usage(person.usage.uhd) },
                {
                  label: t('requests.autoApprove'),
                  value: person.autoApprove.hd ? t('toggle.on') : t('toggle.off'),
                  tone: person.autoApprove.hd ? 'on' : 'off',
                },
                {
                  label: t('requests.autoApprove4k'),
                  value: person.autoApprove.uhd ? t('toggle.on') : t('toggle.off'),
                  tone: person.autoApprove.uhd ? 'on' : 'off',
                },
              ]}
            />
          </div>
        </li>
      {/each}
    </ul>
    {#if canEdit}
      <div class="people-foot">
        <Button onclick={() => (limitsOpen = true)}>
          <Icon name="sliders" size={18} stroke={1.9} />
          {t('requests.editLimits')}
        </Button>
      </div>
    {/if}
  {/if}
</Panel>

{#if canEdit}
  <LimitsDialog bind:open={limitsOpen} onsaved={(p) => onsaved?.(p)} />
{/if}

<style>
  .h2 {
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .note {
    margin: 2px 0 8px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 12px;
  }
  .empty,
  .failed {
    margin-top: 10px;
    font-size: 14px;
    line-height: 20px;
    color: var(--text-3);
  }
  .foot {
    margin-top: 12px;
  }
  .people li {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px 0;
    border-top: 1px solid var(--hairline-strong);
  }
  .avatar {
    flex: 0 0 auto;
    display: flex;
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
  .person {
    flex: 1 1 0;
    min-width: 0;
  }
  .name {
    font-size: 14.5px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .role {
    margin-inline-start: 8px;
    font-size: 12px;
    font-weight: 500;
    color: var(--muted);
  }
  .people-foot {
    padding-top: 16px;
    border-top: 1px solid var(--hairline-strong);
  }
</style>
