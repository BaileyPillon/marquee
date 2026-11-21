<script lang="ts">
  import type { Snippet } from 'svelte';
  import ArtPhone from './ArtPhone.svelte';
  import ArtWide from './ArtWide.svelte';
  import Brand from './Brand.svelte';
  import { isDesktop } from '../core/media';

  interface Props {
    /**
     * setup: the wide scene always, and it stacks above the form on a phone (A-FirstRun).
     * signin: a narrower scene beside the card on desktop; on a phone the short phone scene sits
     * on top and the card fills the screen (A-SignIn). Sign-in and invite share it.
     */
    variant: 'setup' | 'signin';
    title: string;
    intro?: string;
    /** The form (and anything else) under the heading. */
    children: Snippet;
    /** Pinned to the foot of the card on a phone, set under the form on desktop. */
    footer?: Snippet;
  }

  let { variant, title, intro, children, footer }: Props = $props();

  const phone = $derived(variant === 'signin' && !$isDesktop);
  let heading: HTMLElement | undefined = $state();

  /** Move focus to the heading (used when the screen changes under the person's hands). */
  export function focusHeading(): void {
    heading?.focus();
  }
</script>

<div class="screen {variant}" class:phone>
  {#if variant === 'setup'}
    <ArtWide />
  {:else if phone}
    <ArtPhone />
  {:else}
    <ArtWide flex="0 1 480px" />
  {/if}

  <main id="main">
    <div class="card">
      <Brand size={phone ? 'sm' : 'lg'} />
      <h1 bind:this={heading} tabindex="-1">{title}</h1>
      {#if intro}<p class="intro">{intro}</p>{/if}
      <div class="body">{@render children()}</div>
      {#if footer}<section class="foot">{@render footer()}</section>{/if}
    </div>
  </main>
</div>

<style>
  .screen {
    display: flex;
    min-height: 100vh;
    min-height: 100dvh;
    box-sizing: border-box;
    background: var(--ground);
    color: var(--text);
    color-scheme: dark;
  }
  .screen.setup {
    flex-wrap: wrap;
  }
  .screen.phone {
    flex-direction: column;
  }

  main {
    flex: 1 1 420px;
    min-width: 0;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 56px 32px;
    background: var(--ground);
  }
  .phone main {
    flex: 1 1 auto;
    align-items: stretch;
    justify-content: flex-start;
    padding: 20px 24px 12px;
  }

  .card {
    width: 100%;
    max-width: 460px;
  }
  .phone .card {
    flex: 1 1 auto;
    display: flex;
    flex-direction: column;
    align-self: center;
  }

  h1 {
    margin: 36px 0 0;
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: clamp(36px, 6vw, 44px);
    line-height: 1.05;
    letter-spacing: 0.01em;
    color: var(--text);
  }
  h1:focus {
    outline: none;
  }
  .phone h1 {
    margin-top: 10px;
    font-size: 38px;
    line-height: 40px;
  }

  .intro {
    margin: 12px 0 0;
    font-size: 15px;
    line-height: 22px;
    color: var(--text-2);
  }

  .body {
    margin-top: 28px;
  }
  .phone .body {
    /* Room between the form and the foot when the screen is short. */
    margin: 14px 0 24px;
  }

  .foot {
    box-sizing: border-box;
    margin-top: 22px;
    padding-top: 20px;
    border-top: 1px solid var(--hairline);
  }
  .phone .foot {
    margin-top: auto;
    padding-top: 14px;
  }
</style>
