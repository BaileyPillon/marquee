<script lang="ts">
  import PreviewTag from './PreviewTag.svelte';

  interface Props {
    title: string;
    subtitle?: string;
    /** large: the 44px heading under the search bar (Downloads, Settings). */
    scale?: 'default' | 'large';
    /** Space under the header in px. Defaults to 20 (24 when large). */
    gap?: number;
    /** The page still shows sample content: a small "Preview" tag follows the heading. */
    preview?: boolean;
  }

  let { title, subtitle, scale = 'default', gap, preview = false }: Props = $props();
</script>

<header class="head {scale}" style:margin-bottom={gap === undefined ? undefined : `${gap}px`}>
  <h1>
    {title}
    {#if preview}<span class="tag"><PreviewTag /></span>{/if}
  </h1>
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
  .tag {
    display: inline-block;
    margin-left: 6px;
    vertical-align: middle;
  }
  p {
    margin-top: 6px;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: var(--muted);
  }

  .large {
    margin: 4px 0 24px;
  }
  .large h1 {
    font-size: 44px;
    line-height: 1.05;
  }
  .large p {
    margin-top: 8px;
  }

  @media (max-width: 899.98px) {
    h1,
    .large h1 {
      font-size: 34px;
    }
  }
</style>
