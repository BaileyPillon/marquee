import { mount } from 'svelte';
import './lib/core/tokens.css';
import './lib/core/fonts.css';
import './lib/core/base.css';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('missing #app element');

export default mount(App, { target });
