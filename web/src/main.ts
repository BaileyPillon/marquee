import { mount } from 'svelte';
import './lib/core/tokens.css';
import './lib/core/fonts.css';
import './lib/core/base.css';
import App from './App.svelte';
import { initLanguage } from './lib/core/i18n';

const target = document.getElementById('app');
if (!target) throw new Error('missing #app element');

// Pick the language (and download its catalog) before the first render, so nothing flashes in English.
try {
  await initLanguage();
} catch {
  /* English is already in the bundle */
}

export default mount(App, { target });
