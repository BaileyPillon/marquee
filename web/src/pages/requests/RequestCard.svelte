<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LabelValue from '../../lib/ui/LabelValue.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import Poster from '../../lib/ui/Poster.svelte';
  import { titlePath } from '../../lib/core/catalog';
  import { t } from '../../lib/core/i18n';
  import type { RequestItem } from '../../lib/core/requestsApi';
  import { askerFirst, askFacts, askWhat, posterYearLine, whoWhen } from '../../lib/core/requestView';

  interface Props {
    request: RequestItem;
    busy: boolean;
    onapprove: () => void;
    ondecline: () => void;
    /** Approve and turn on auto-approve for 4K for this person. Only offered for 4K asks. */
    onalways?: () => void;
  }

  let { request, busy, onapprove, ondecline, onalways }: Props = $props();

  const is4k = $derived(request.quality === 'uhd');
  const who = $derived(askerFirst(request));
  const names = $derived({ who, title: request.title.name });
  const facts = $derived(askFacts(request).facts);
</script>

<Panel tag="article" padding="20px">
  <div class="card">
    <div class="top">
      <!-- The poster is a picture of the title; the heading beside it names it. -->
      <div aria-hidden="true">
        <Poster
          title={request.title.name}
          year={posterYearLine(request)}
          status={{ kind: 'requested' }}
          size="mini"
          imageRef={request.title.posterRef || undefined}
        />
      </div>
      <div class="who">
        <div class="when">{whoWhen(request)}</div>
        <h3>
          <a href={titlePath(request.title.kind, request.title.id)}>{request.title.name}</a>
          {#if request.title.year}<span class="year">({request.title.year})</span>{/if}
        </h3>
        <div class="what">{askWhat(request)}</div>
      </div>
    </div>

    <LabelValue items={facts} gap={16} />

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
      {#if is4k && onalways}
        <Button variant="link" disabled={busy} onclick={onalways}>
          {t('requests.alwaysAllow', { who, quality: t('ask.quality.uhd.short') })}
        </Button>
      {/if}
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
    overflow-wrap: anywhere;
  }
  h3 a {
    color: inherit;
    text-decoration: none;
  }
  h3 a:hover {
    text-decoration: underline;
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
