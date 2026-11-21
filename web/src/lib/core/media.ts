import { readable, type Readable } from 'svelte/store';

/** A store that follows a CSS media query. The first value is read synchronously. */
export function mediaQuery(query: string): Readable<boolean> {
  const supported = typeof matchMedia === 'function';
  return readable(supported ? matchMedia(query).matches : true, (set) => {
    if (!supported) return;
    const mql = matchMedia(query);
    const update = () => set(mql.matches);
    update();
    mql.addEventListener('change', update);
    return () => mql.removeEventListener('change', update);
  });
}

/** True at 900px and wider: rail layout. Below it: compact header and bottom tabs. */
export const isDesktop = mediaQuery('(min-width: 900px)');
