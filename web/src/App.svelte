<script lang="ts">
  import { onMount, tick } from 'svelte';
  import NotFound from './pages/NotFound.svelte';
  import { pages } from './pages/registry';
  import Stub from './pages/Stub.svelte';
  import PhoneHeader from './lib/ui/PhoneHeader.svelte';
  import Rail from './lib/ui/Rail.svelte';
  import TabBar from './lib/ui/TabBar.svelte';
  import TopBar from './lib/ui/TopBar.svelte';
  import { ping } from './lib/core/api';
  import { t } from './lib/core/i18n';
  import { isDesktop } from './lib/core/media';
  import { requestsState } from './lib/core/requests.svelte';
  import { current, navigate, startRouter, type Section } from './lib/core/router';
  import { session } from './lib/core/session.svelte';

  const match = $derived($current);
  let main: HTMLElement | undefined = $state();
  let offline = $state(false);

  // The Requests badge is asks waiting for the owner's yes, from `/requests/summary` once that
  // exists. An owner or an admin sees it; with no answer (not built yet, or offline) there is none.
  const badges = $derived<Partial<Record<Section, number>>>(
    (session.me?.role === 'owner' || session.me?.role === 'admin') && requestsState.waiting > 0
      ? { requests: requestsState.waiting }
      : {},
  );

  // Keep the summary fresh while someone is signed in; forget it when they leave.
  $effect(() => {
    if (!session.me) {
      requestsState.clear();
      return;
    }
    return requestsState.start();
  });

  // The rail's owner block and the phone avatar: the real person, once the server has said who.
  const owner = $derived(
    session.me
      ? {
          name: session.me.name,
          initial: ([...session.me.name.trim()][0] ?? '?').toUpperCase(),
          role: session.me.role,
        }
      : undefined,
  );

  onMount(() => {
    const stop = startRouter();
    void session.start();
    return stop;
  });

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
    // Wait for the new page to be in place (the shell may have just appeared) before focusing.
    void tick().then(() => main?.focus({ preventScroll: true }));
  });

  // Is the server there? Home keeps showing mock data either way.
  $effect(() => {
    let alive = true;
    let timer: number | undefined;
    const controller = new AbortController();

    async function check(): Promise<void> {
      const up = await ping(controller.signal);
      if (!alive) return;
      offline = !up;
      if (up) {
        // The server came back after we could not ask it who we are.
        if (session.status === 'offline') void session.start();
      } else {
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

{#if session.status === 'loading'}
  <!-- Waiting to hear who this is: show nothing rather than a page that may be about to change. -->
  <div class="boot" role="status"><span class="sr-only">{t('boot.loading')}</span></div>
{:else if match.route.fullScreen}
  <!-- Setup, sign-in and invite: no rail, no tabs. The page draws its own whole screen. -->
  {@const Page = pages[match.route.name] ?? Stub}
  <Page {match} />
{:else}
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

        {#if match.route.name === 'notFound'}
          <NotFound />
        {:else}
          {@const Page = pages[match.route.name] ?? Stub}
          <!-- Keyed by route, so /movies and /tv (one component) start fresh each time. -->
          {#key match.route.name}
            <Page {match} />
          {/key}
        {/if}
      </main>
    </div>

    {#if !$isDesktop}
      <TabBar active={match.route.section} role={session.me?.role ?? null} {badges} />
    {/if}
  </div>
{/if}

<style>
  .boot {
    min-height: 100vh;
    min-height: 100dvh;
  }
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
