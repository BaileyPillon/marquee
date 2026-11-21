// Mock data for Settings › Connections, reproduced from the approved Cinema canvas
// (A-Settings.dc.html). getSettings() has the shape of the future API call.
import type { SettingsView } from '../core/views';
import { respond } from './latency';

const settings: SettingsView = {
  downloadApps: [
    { name: 'qBittorrent', status: 'Connected · http://localhost:8080', state: 'on', action: 'test' },
    { name: 'SABnzbd', status: 'Connected', state: 'on', action: 'test' },
    { name: 'Deluge', status: 'Not set up', state: 'off', action: 'add' },
  ],
  mediaServers: [
    {
      name: 'Jellyfin',
      status: 'Connected · rescans after every import',
      state: 'on',
      action: 'test',
    },
    {
      name: 'Plex',
      status: "Signed in as Bailey · reads 3 friends' watchlists every 15 min",
      state: 'on',
      action: 'edit',
    },
  ],
  sources: [
    {
      name: 'Archive feed',
      tag: 'Torznab',
      status: 'Working · 18 searches today',
      state: 'on',
      action: 'test',
    },
    {
      name: 'Open movies',
      tag: 'Torznab',
      status: 'Working · 4 searches today',
      state: 'on',
      action: 'test',
    },
    { name: 'Usenet indexer', tag: 'Newznab', status: 'Key saved', state: 'on', action: 'edit' },
  ],
  titleInfo: [
    { name: 'TMDB', status: 'Your own key · last used 2 min ago', state: 'on', action: 'edit' },
    { name: 'TVmaze', status: 'No key needed', state: 'on', action: 'test' },
    { name: 'MusicBrainz', status: 'No key needed', state: 'on', action: 'test' },
  ],
  keys: [
    {
      name: "Bailey's laptop",
      status: 'Created 20 Nov · last used 1 h ago',
      abilities: [
        { id: 'see', allowed: true },
        { id: 'ask', allowed: true },
        { id: 'downloads', allowed: false },
        { id: 'settings', allowed: false },
        { id: 'delete', allowed: false },
        { id: 'approve', allowed: false },
      ],
    },
  ],
  alerts: [
    { name: 'Discord', status: 'On · new asks, failures', state: 'on', action: 'edit' },
    { name: 'Email', status: 'Off', state: 'off', action: 'setup' },
  ],
};

/** The Connections tab. The API will answer GET /api/v1/settings/connections. */
export function getSettings(): Promise<SettingsView> {
  return respond(settings);
}
