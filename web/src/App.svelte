<script lang="ts">
  import { onMount } from 'svelte';
  import Home from './pages/Home.svelte';
  import NotFound from './pages/NotFound.svelte';
  import Stub from './pages/Stub.svelte';
  import PhoneHeader from './lib/ui/PhoneHeader.svelte';
  import Rail from './lib/ui/Rail.svelte';
  import TabBar from './lib/ui/TabBar.svelte';
  import TopBar from './lib/ui/TopBar.svelte';
  import { health } from './lib/core/api';
  import { t } from './lib/core/i18n';
  import { isDesktop } from './lib/core/media';
  import { current, navigate, startRouter } from './lib/core/router';
  import { badges, owner } from './lib/mock/shell';

  const match = $derived($current);
  let main: HTMLElement | undefined = $state();
  let offline = $state(false);

  onMount(() => startRouter());

  // Tab title follows the route.
  $effect(() => {
    document.title =
      match.route.name === 'home'
        ? t('app.name')
        : t('doc.title', { page: t(match.route.title), app: t('app.name') });
  });

  // After a route change, start at the top and move focus into the new page.
  let firstRoute = true;
  $effect(() => {
    void match.path;
    if (firstRoute) {
      firstRoute = false;
      return;
    }
    window.scrollTo(0, 0);
    main?.focus({ preventScroll: true });
  });

  // Is the server there? Home keeps showing mock data either way.
  $effect(() => {
    let alive = true;
    let timer: number | undefined;
    const controller = new AbortController();

    async function check(): Promise<void> {
      try {
        await health(controller.signal);
        if (alive) offline = false;
      } catch {
        if (!alive) return;
        offline = true;
        timer = window.setTimeout(check, 15000);
      }
    }

    const recheck = (): void => {
      window.clearTimeout(timer);
      void check();
    };
    void check();
    window.addEventListener('online', recheck);
    return () => {
      alive = false;
      controller.abort();
      window.clearTimeout(timer);
      window.removeEventListener('online', recheck);
    };
  });

  function skip(event: MouseEvent): void {
    event.preventDefault();
    main?.focus();
  }
</script>

<a class="skip" href="#main" onclick={skip}>{t('a11y.skip')}</a>

<div class="shell" class:phone={!$isDesktop}>
  {#if $isDesktop}
    <Rail active={match.route.section} {badges} {owner} {offline} />
  {/if}

  <div class="col">
    {#if !$isDesktop}
      <PhoneHeader {owner} {offline} />
    {/if}

    <main id="main" tabindex="-1" bind:this={main}>
      {#if $isDesktop && match.route.topBar}
        <TopBar onask={() => navigate('/explore')} />
      {/if}

      {#if match.route.name === 'home'}
        <Home />
      {:else if match.route.name === 'notFound'}
        <NotFound />
      {:else}
        <Stub {match} />
      {/if}
    </main>
  </div>

  {#if !$isDesktop}
    <TabBar active={match.route.section} />
  {/if}
</div>

<style>
  .shell {
    display: flex;
    min-height: 100vh;
    min-height: 100dvh;
  }
  .col {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
  }
  main {
    flex: 1 1 auto;
    box-sizing: border-box;
    min-width: 0;
    padding: 20px 32px 40px;
  }

  /* Phone: header on top, tabs fixed at the foot. */
  .phone main {
    padding: 8px 16px calc(var(--tabbar-height) + env(safe-area-inset-bottom) + 24px);
  }

  .skip {
    position: absolute;
    left: 8px;
    top: 8px;
    z-index: 100;
    padding: 10px 14px;
    border-radius: var(--radius-sm);
    background: var(--accent);
    color: var(--on-accent);
    font-size: 14px;
    font-weight: 700;
    text-decoration: none;
    transform: translateY(-200%);
  }
  .skip:focus {
    transform: none;
    color: var(--on-accent);
  }
</style>
