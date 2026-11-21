<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LabelValue from '../../lib/ui/LabelValue.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import Poster from '../../lib/ui/Poster.svelte';
  import { t } from '../../lib/core/i18n';
  import type { WaitingRequest } from '../../lib/core/views';

  interface Props {
    request: WaitingRequest;
    busy: boolean;
    onapprove: () => void;
    ondecline: () => void;
  }

  let { request, busy, onapprove, ondecline }: Props = $props();

  const is4k = $derived(request.quality === '4K');
  const names = $derived({ who: request.requester, title: request.title });
</script>

<Panel tag="article" padding="20px">
  <div class="card">
    <div class="top">
      <!-- The poster is a picture of the title; the heading beside it names it. -->
      <div aria-hidden="true">
        <Poster
          title={request.title}
          year={request.posterYear}
          status={{ kind: 'requested' }}
          size="mini"
        />
      </div>
      <div class="who">
        <div class="when">{request.who}</div>
        <h3>
          <span>{request.title}</span>
          <span class="year">({request.year})</span>
        </h3>
        <div class="what">{request.what}</div>
      </div>
    </div>

    <LabelValue items={request.facts} gap={16} />

    <div class="actions">
      <div class="row">
        <Button
          variant="primary"
          disabled={busy}
          aria-label={is4k ? t('requests.approveAria4k', names) : t('requests.approveAria', names)}
          onclick={onapprove}
        >
          <Icon name="check" size={18} stroke={2.4} />
          {t('action.approve')}
        </Button>
        <Button
          disabled={busy}
          aria-label={is4k ? t('requests.declineAria4k', names) : t('requests.declineAria', names)}
          onclick={ondecline}
        >
          <Icon name="close" size={18} stroke={2.2} />
          {t('action.decline')}
        </Button>
      </div>
      <Button variant="link" disabled={busy} onclick={onapprove}>
        {t('requests.alwaysAllow', { who: request.requester, quality: request.quality })}
      </Button>
    </div>
  </div>
</Panel>

<style>
  .card {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .top {
    display: flex;
    align-items: flex-start;
    gap: 16px;
  }
  .who {
    flex: 1 1 0;
    min-width: 0;
  }
  .when {
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    color: var(--muted);
  }
  h3 {
    margin-top: 2px;
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .year {
    margin-left: 6px;
    font-weight: 500;
    color: var(--text-3);
  }
  .what {
    margin-top: 4px;
    font-size: 14px;
    font-weight: 500;
    line-height: 20px;
    color: var(--text-2);
  }
  .actions {
    margin-top: auto;
    padding-top: 16px;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
  }
  .row :global(.btn) {
    flex: 1 1 130px;
    padding: 0 18px;
  }
  .actions > :global(.btn) {
    margin-top: 6px;
  }
</style>
