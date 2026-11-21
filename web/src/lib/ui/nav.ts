import type { MessageKey } from '../core/i18n';
import type { Section } from '../core/router';
import type { IconName } from './icons';

export interface NavItem {
  section: Section;
  href: string;
  label: MessageKey;
  icon: IconName;
}

export interface NavGroup {
  id: string;
  /** Group heading; the first group has none. */
  label?: MessageKey;
  items: NavItem[];
}

/** Rail groups, in the order of the Cinema mockup. */
export const navGroups: NavGroup[] = [
  {
    id: 'discover',
    items: [
      { section: 'home', href: '/', label: 'nav.home', icon: 'home' },
      { section: 'explore', href: '/explore', label: 'nav.explore', icon: 'explore' },
      { section: 'popular', href: '/popular', label: 'nav.popular', icon: 'popular' },
    ],
  },
  {
    id: 'library',
    label: 'nav.group.library',
    items: [
      { section: 'movies', href: '/movies', label: 'nav.movies', icon: 'movies' },
      { section: 'tv', href: '/tv', label: 'nav.tv', icon: 'tv' },
      { section: 'music', href: '/music', label: 'nav.music', icon: 'music' },
    ],
  },
  {
    id: 'yours',
    label: 'nav.group.yourStuff',
    items: [
      { section: 'requests', href: '/requests', label: 'nav.requests', icon: 'requests' },
      { section: 'downloads', href: '/downloads', label: 'nav.downloads', icon: 'downloads' },
      { section: 'calendar', href: '/calendar', label: 'nav.calendar', icon: 'calendar' },
    ],
  },
];

/** Settings sits at the foot of the rail (as on the Downloads screen of the canvas). */
export const settingsItem: NavItem = {
  section: 'settings',
  href: '/settings',
  label: 'nav.settings',
  icon: 'settings',
};

/** Phone bottom tabs, as on the phone Home of the canvas. */
export const tabItems: NavItem[] = [
  { section: 'home', href: '/', label: 'tabs.home', icon: 'home' },
  { section: 'explore', href: '/explore', label: 'tabs.explore', icon: 'explore' },
  { section: 'requests', href: '/requests', label: 'tabs.asks', icon: 'requests' },
  { section: 'settings', href: '/settings', label: 'tabs.profile', icon: 'profile' },
];
