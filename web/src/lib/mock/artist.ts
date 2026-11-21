// Mock data for the Artist page, reproduced from the approved Cinema canvas (A-Artist.dc.html).
// getArtist() has the shape of the future API call; any id answers with the same artist.
import type { Album, ArtistView } from '../core/views';
import { respond } from './latency';
import { downloading, library, missing, requested } from './poster';

const CAPTION = 'Public-domain recording';

function album(
  title: string,
  years: string,
  status: Album['status'],
  tracks: number,
  color: string,
): Album {
  return { id: title.toLowerCase(), title, years, status, tracks, caption: CAPTION, color };
}

const chopin: ArtistView = {
  id: 'frederic-chopin',
  name: 'Frédéric Chopin',
  shortName: 'Chopin',
  born: 1810,
  died: 1849,
  status: 'In your library · 3 of 6 albums',
  meta: 'Composer · 1810–1849 · Romantic',
  blurb:
    'Frédéric Chopin was a Polish composer and pianist, born near Warsaw and based in Paris from 1831. He wrote mostly for solo piano, including nocturnes, études, waltzes and mazurkas.',
  theme: { bg: '#2B2142', artBg: '#1B1329' },
  artCaption: 'NOCTURNES · BALLADES',
  server: 'Jellyfin',
  ownerName: 'Bailey',
  albums: {
    note: '3 of 6 in your library',
    items: [
      album('Nocturnes', '1830–1846', library, 21, 'var(--poster-metropolis)'),
      album('Ballades', '1831–1842', library, 4, 'var(--poster-nosferatu)'),
      album('Préludes, Op. 28', '1839', downloading(30), 24, 'var(--poster-sherlock-jr)'),
      album('Études, Op. 10', '1833', requested, 12, 'var(--poster-safety-last)'),
      album('Waltzes', '1831–1847', library, 19, 'var(--poster-his-girl-friday)'),
      album('Mazurkas', '1825–1849', missing, 58, 'var(--poster-spring)'),
    ],
  },
  tracks: {
    shown: 6,
    total: 44,
    items: [
      {
        num: 1,
        title: 'Nocturne in E-flat major, Op. 9 No. 2',
        album: 'Nocturnes',
        length: '4:34',
        format: 'FLAC',
      },
      {
        num: 2,
        title: 'Nocturne in C-sharp minor, Op. posth.',
        album: 'Nocturnes',
        length: '4:20',
        format: 'FLAC',
      },
      {
        num: 3,
        title: 'Ballade No. 1 in G minor, Op. 23',
        album: 'Ballades',
        length: '9:21',
        format: 'FLAC',
      },
      {
        num: 4,
        title: 'Ballade No. 4 in F minor, Op. 52',
        album: 'Ballades',
        length: '10:08',
        format: 'MP3 320',
      },
      {
        num: 5,
        title: 'Waltz in C-sharp minor, Op. 64 No. 2',
        album: 'Waltzes',
        length: '3:30',
        format: 'FLAC',
      },
      {
        num: 6,
        title: 'Waltz in D-flat major, Op. 64 No. 1 ("Minute")',
        album: 'Waltzes',
        length: '1:52',
        format: 'MP3 320',
      },
    ],
  },
  similar: [
    { id: 'franz-liszt', name: 'Franz Liszt', detail: 'Composer · 1811–1886', color: 'var(--poster-nosferatu)' },
    {
      id: 'robert-schumann',
      name: 'Robert Schumann',
      detail: 'Composer · 1810–1856',
      color: 'var(--poster-sherlock-jr)',
    },
    {
      id: 'felix-mendelssohn',
      name: 'Felix Mendelssohn',
      detail: 'Composer · 1809–1847',
      color: 'var(--poster-safety-last)',
    },
    {
      id: 'clara-schumann',
      name: 'Clara Schumann',
      detail: 'Pianist · 1819–1896',
      color: 'var(--poster-his-girl-friday)',
    },
    { id: 'erik-satie', name: 'Erik Satie', detail: 'Composer · 1866–1925', color: 'var(--poster-the-kid)' },
  ],
};

/** The artist page for `id`. The API will answer GET /api/v1/artists/:id. */
export function getArtist(_id: string): Promise<ArtistView> {
  return respond(chopin);
}
