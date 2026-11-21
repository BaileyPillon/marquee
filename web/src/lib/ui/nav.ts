import type { MessageKey } from '../core/i18n';
import type { Section } from '../core/router';
import type { IconName } from './icons';

export interface NavItem {
  section: Section;
  href: string;
  label: MessageKey;
  icon: IconName;
  /** Stroke width of the icon, for a glyph (the three dots) that needs a heavier line. */
  iconStroke?: number;
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

/**
 * Phone bottom tabs, as on the phone Home of the canvas. They differ by role: an owner or admin
 * runs the server (Requests to decide, Downloads to watch, More for Settings); a member asks for
 * things and sees their own asks and profile.
 */
const memberTabs: NavItem[] = [
  { section: 'home', href: '/', label: 'tabs.home', icon: 'home' },
  { section: 'explore', href: '/explore', label: 'tabs.explore', icon: 'explore' },
  { section: 'requests', href: '/requests', label: 'tabs.asks', icon: 'requests' },
  { section: 'settings', href: '/settings', label: 'tabs.profile', icon: 'profile' },
];

const managerTabs: NavItem[] = [
  { section: 'home', href: '/', label: 'tabs.home', icon: 'home' },
  { section: 'explore', href: '/explore', label: 'tabs.explore', icon: 'explore' },
  { section: 'requests', href: '/requests', label: 'tabs.requests', icon: 'requests' },
  { section: 'downloads', href: '/downloads', label: 'tabs.downloads', icon: 'downloads' },
  { section: 'settings', href: '/settings', label: 'tabs.more', icon: 'more', iconStroke: 3 },
];

export function tabItemsFor(role: 'owner' | 'admin' | 'member' | null | undefined): NavItem[] {
  return role === 'owner' || role === 'admin' ? managerTabs : memberTabs;
}
