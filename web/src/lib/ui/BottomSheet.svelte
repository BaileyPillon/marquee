<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /** Whether the sheet is showing. Bind it: the sheet sets it to false when it closes itself. */
    open: boolean;
    /** The dialog's heading. The dialog is labelled by it. */
    title: string;
    /** Called after the sheet has closed, whichever way it closed. */
    onclose?: () => void;
    /** A wider dialog on desktop (a table of results); on a phone every sheet is full width. */
    wide?: boolean;
    children: Snippet;
  }

  let { open = $bindable(false), title, onclose, wide = false, children }: Props = $props();

  const uid = $props.id();
  const titleId = `sheet-title-${uid}`;

  let dialog: HTMLDialogElement | undefined = $state();
  /** What had focus when the sheet opened: focus goes back there when it closes. */
  let opener: HTMLElement | null = null;
  let overflowBefore = '';

  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  // The <dialog> element is the modal: showModal() puts it above the page, makes the page inert
  // and handles Esc. The effect only keeps it in step with `open`.
  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) {
      opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      overflowBefore = document.documentElement.style.overflow;
      document.documentElement.style.overflow = 'hidden';
      dialog.showModal();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  });

  // Never leave the page locked if the sheet goes away while open.
  $effect(() => () => {
    document.documentElement.style.overflow = overflowBefore;
  });

  /** Runs when the dialog has closed: by Esc, by the backdrop, by a button, or by `open = false`. */
  function closed(): void {
    document.documentElement.style.overflow = overflowBefore;
    open = false;
    if (opener?.isConnected) opener.focus({ preventScroll: true });
    opener = null;
    onclose?.();
  }

  function onbackdrop(event: MouseEvent): void {
    // The dialog has no padding of its own, so a click that lands on it itself is a click on the backdrop.
    if (event.target === dialog) dialog?.close();
  }

  /** Keep Tab and Shift+Tab inside the sheet. */
  function ontab(event: KeyboardEvent): void {
    if (event.key !== 'Tab' || !dialog) return;
    const items = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
    );
    const first = items[0];
    const last = items[items.length - 1];
    if (!first || !last) {
      event.preventDefault();
      return;
    }
    const active = document.activeElement;
    if (event.shiftKey && (active === first || active === dialog)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<!--
  A native <dialog> opened with showModal(): the page behind goes inert and Esc closes it. The
  explicit role and aria-modal keep older assistive tech in step.
-->
<!-- svelte-ignore a11y_no_redundant_roles, a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog
  bind:this={dialog}
  class="sheet"
  class:wide
  role="dialog"
  aria-modal="true"
  aria-labelledby={titleId}
  onclose={closed}
  onclick={onbackdrop}
  onkeydown={ontab}
>
  <div class="inner">
    <div class="handle" aria-hidden="true"></div>
    <h2 id={titleId}>{title}</h2>
    {#if open}{@render children()}{/if}
  </div>
</dialog>

<style>
  .sheet {
    /* Reset the browser's dialog box: the content wrapper carries the padding. */
    box-sizing: border-box;
    width: 100%;
    max-width: 100%;
    max-height: 92dvh;
    margin: auto 0 0;
    padding: 0;
    overflow: auto;
    border: 0;
    border-radius: 16px 16px 0 0;
    background: var(--panel);
    box-shadow: inset 0 0 0 1px var(--hairline-strong);
    color: var(--text);
    animation: rise 0.2s ease-out;
  }
  .sheet::backdrop {
    background: var(--overlay);
  }
  .inner {
    padding: 10px 20px calc(20px + env(safe-area-inset-bottom));
  }
  .handle {
    width: 40px;
    height: 4px;
    margin: 0 auto 14px;
    border-radius: 2px;
    background: var(--sheet-handle);
  }
  h2 {
    font-size: 22px;
    font-weight: 700;
    line-height: 28px;
    color: var(--text);
  }

  @keyframes rise {
    from {
      transform: translateY(24px);
      opacity: 0;
    }
  }

  /* Desktop: a centred dialog. */
  @media (min-width: 900px) {
    .sheet {
      width: min(480px, calc(100vw - 32px));
      margin: auto;
      border-radius: var(--radius-xl);
      box-shadow:
        inset 0 0 0 1px var(--hairline-strong),
        0 24px 60px rgba(0, 0, 0, 0.5);
    }
    .sheet.wide {
      width: min(960px, calc(100vw - 32px));
    }
    .inner {
      padding: 24px 24px 24px;
    }
    .handle {
      display: none;
    }
  }
</style>
