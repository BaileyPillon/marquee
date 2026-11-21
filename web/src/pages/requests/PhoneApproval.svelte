<script lang="ts">
  import Button from '../../lib/ui/Button.svelte';
  import Chip from '../../lib/ui/Chip.svelte';
  import Icon from '../../lib/ui/Icon.svelte';
  import LabelValue from '../../lib/ui/LabelValue.svelte';
  import Panel from '../../lib/ui/Panel.svelte';
  import Thumb from '../../lib/ui/Thumb.svelte';
  import { posterColor } from '../../lib/core/posters';
  import { t } from '../../lib/core/i18n';
  import type { WaitingRequest } from '../../lib/core/views';

  interface Props {
    /** The ask on the big card. */
    active: WaitingRequest;
    /** The other asks waiting, in a short list under it. */
    others: WaitingRequest[];
    busy: boolean;
    onapprove: () => void;
    ondecline: () => void;
    /** Put another ask on the big card. */
    onpick: (id: string) => void;
  }

  let { active, others, busy, onapprove, ondecline, onpick }: Props = $props();

  const uid = $props.id();
  const alsoId = `phone-also-${uid}`;

  // The title is set big; a long one steps down so it still sits on the card.
  const titleSize = $derived(active.title.length <= 12 ? 58 : active.title.length <= 20 ? 42 : 34);
  const heading = $derived(
    active.quality === '4K'
      ? t('requests.phone.asked4k', { who: active.requester, title: active.title })
      : t('requests.phone.asked', { who: active.requester, title: active.title }),
  );
</script>

<Panel label={t('requests.phone.cardLabel')} padding="16px" radius="xl">
  <div class="big" style:background={posterColor(active.title)}>
    <div class="frame" aria-hidden="true"></div>
    <div class="row">
      <Chip tone="dashed" size="status" scrim>{t('status.requested')}</Chip>
      <span class="quality">{active.quality}</span>
    </div>
    <div class="base">
      <div class="rule"></div>
      <div class="title" style:font-size="{titleSize}px">{active.title}</div>
      <div class="meta">{active.posterMeta}</div>
    </div>
  </div>

  <h2>{heading}</h2>
  <p class="ago">{t('requests.phone.ago', { ago: active.ago })}</p>

  <LabelValue items={active.phoneFacts} density="cozy" gap={12} />

  <div class="decide">
    <Button variant="primary" size="xl" disabled={busy} onclick={onapprove}>
      <Icon name="check" size={20} stroke={2.4} />
      {t('action.approve')}
    </Button>
    <Button size="xl" disabled={busy} onclick={ondecline}>
      <Icon name="close" size={20} stroke={2.4} />
      {t('action.decline')}
    </Button>
  </div>
</Panel>

{#if others.length > 0}
  <section class="also" aria-labelledby={alsoId}>
    <h2 id={alsoId}>{t('requests.phone.also')}</h2>
    <ul>
      {#each others as other (other.id)}
        <li>
          <button type="button" onclick={() => onpick(other.id)}>
            <Thumb title={other.title} width={40} height={52} fontSize={26} />
            <span class="text">
              <span class="name">{other.title}</span>
              <span class="detail">{other.brief}</span>
            </span>
            <span class="chev"><Icon name="chevron" size={20} stroke={2} /></span>
          </button>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<style>
  .big {
    position: relative;
    box-sizing: border-box;
    min-height: 176px;
    padding: 16px;
    border-radius: var(--radius-md);
    overflow: hidden;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    gap: 12px;
    box-shadow: inset 0 0 0 1px var(--poster-edge);
  }
  .frame {
    position: absolute;
    inset: 8px;
    box-sizing: border-box;
    border: 1px solid rgba(242, 237, 228, 0.18);
    border-radius: 4px;
  }
  .row {
    position: relative;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 8px;
  }
  .quality {
    box-sizing: border-box;
    padding: 2px 9px;
    border: 1px solid var(--line-soft);
    border-radius: 4px;
    font-size: 13px;
    font-weight: 700;
    line-height: 18px;
    letter-spacing: 0.08em;
    color: var(--text);
  }
  .base {
    position: relative;
  }
  .rule {
    width: 36px;
    height: 1px;
    margin-bottom: 10px;
    background: var(--line-strong);
  }
  .title {
    font-family: var(--font-serif);
    font-weight: 400;
    line-height: 1;
    letter-spacing: 0.005em;
    color: var(--text);
  }
  .meta {
    margin-top: 8px;
    font-size: 13px;
    font-weight: 500;
    line-height: 16px;
    letter-spacing: 0.04em;
    color: var(--text);
  }

  h2 {
    margin-top: 14px;
    font-size: 17px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
  }
  .ago {
    margin-top: 2px;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .decide {
    display: flex;
    gap: 10px;
    margin-top: 16px;
  }
  .decide :global(.btn) {
    flex: 1 1 0;
  }

  .also {
    margin-top: 18px;
  }
  .also h2 {
    margin: 0 0 10px;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .also ul {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  button {
    display: flex;
    align-items: center;
    gap: 12px;
    box-sizing: border-box;
    width: 100%;
    min-height: 64px;
    padding: 6px 14px 6px 6px;
    border: 0;
    border-radius: var(--radius-lg);
    background: var(--panel);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
    color: var(--text);
    text-align: left;
  }
  button:hover {
    background: var(--panel-hover);
  }
  .text {
    flex: 1 1 auto;
    min-width: 0;
  }
  .name {
    display: block;
    font-size: 15px;
    font-weight: 600;
    line-height: 20px;
    color: var(--text);
  }
  .detail {
    display: block;
    font-size: 13px;
    line-height: 18px;
    color: var(--muted);
  }
  .chev {
    display: flex;
    color: var(--muted);
  }
</style>
