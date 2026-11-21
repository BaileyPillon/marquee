<script lang="ts">
  interface Props {
    title: string;
    subtitle?: string;
  }

  let { title, subtitle }: Props = $props();
</script>

<header class="head">
  <h1>{title}</h1>
  {#if subtitle}<p>{subtitle}</p>{/if}
</header>

<style>
  .head {
    margin-bottom: 20px;
  }
  h1 {
    font-family: var(--font-serif);
    font-weight: 400;
    font-size: 40px;
    line-height: 1.1;
    letter-spacing: 0.01em;
    color: var(--text);
  }
  p {
    margin-top: 6px;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: var(--muted);
  }

  @media (max-width: 899.98px) {
    h1 {
      font-size: 34px;
    }
  }
</style>
