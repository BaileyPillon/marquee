# Privacy

Marquee runs on your machine and keeps everything there: your library, your people, your asks, your keys. This page
lists **every outside address Marquee can contact**, when, what it sends, and how to switch it off.

**The short version**

- **No telemetry.** No analytics, no crash reports, no "phone home".
- **No update check.** Marquee never asks GitHub or anyone whether a new version exists. You update when you choose.
- **No relay.** Titles come straight from the providers below with your own key where one is needed. Marquee never
  calls a metadata server run by the project, or by anyone else's app.
- **Your library is never uploaded.** Providers see what you search for and which titles you look up. The subtitle
  providers also see the title, and a fingerprint, of each video they are asked to find subtitles for.
- **Your browser talks only to Marquee.** The page loads its fonts and scripts from Marquee itself, and posters
  come through Marquee, so TMDB or TVmaze never see your browser. The one exception is a link you click
  yourself, such as a title's **Trailer** button, which opens YouTube in a new tab.
- **Outside calls go straight out.** Marquee does not read `HTTP_PROXY` and friends, so a proxy you set up for other
  programs is not used. Every call goes through one client with timeouts, a limit per host and a log line at
  debug level (the address without its query, never a key or a header).

Every call Marquee makes identifies itself as `Marquee/<version> (+https://github.com/BaileyPillon/marquee)`.
That is the only thing in the project's name that leaves your machine.

## The providers and services

| Host | Used for | When | What is sent | Turn it off |
|---|---|---|---|---|
| `api.themoviedb.org` (TMDB) | Movie details, cast, trailers | When you search for movies, add or refresh a movie, or a watchlist item is matched | The text you searched for, TMDB ids, and **your TMDB key** | Leave the key out, or clear it in Settings → Title info. Movie lookups then stop; shows and music still work. |
| `image.tmdb.org` | Movie posters and backdrops | When a movie page or list needs an image | The image's path. Marquee downloads it once and keeps it | Same as above |
| `api.tvmaze.com` | Show details, seasons, episodes, air dates | When you search for shows, or add or refresh a show | The text you searched for, TVmaze ids | No switch. See below. |
| `static.tvmaze.com` | Show posters | When a show page needs an image | The image's path | No switch |
| `musicbrainz.org` | Artists, albums, tracks | When you search for artists, add or refresh an artist | The text you searched for, MusicBrainz ids. At most one request a second, as MusicBrainz asks | No switch |
| `coverartarchive.org`, which redirects to `archive.org` | Album covers | When an album needs a cover | The image's path (a release id) | No switch |
| `plex.tv`, `discover.provider.plex.tv`, `community.plex.tv` | Plex watchlists | **Only if you link Plex.** A check every 15 minutes | A random id Marquee made for itself, the name "Marquee", and your Plex token; it reads your watchlist, your friends' watchlists and the ids of the items | Settings → Connections → Watchlists → unlink Plex, or switch the check off (`PUT /api/v1/watchlists/plex` with `{"enabled": false}`) |
| `api.opensubtitles.com` (OpenSubtitles.com) | Subtitles | **Only if you add your OpenSubtitles key** and turn that provider on. A search after each import, then on a schedule for files still missing a language | What a subtitle search needs: the title, year, IMDb or TMDB id, season and episode, the languages you chose, and a **fingerprint of the video file** (see below); your key, and your username and password if you saved them | Clear the key, or switch OpenSubtitles off in Settings → Subtitles |
| `www.podnapisi.net` (Podnapisi) | Subtitles | **On by default**, because it needs no key. A search after each import, then on a schedule for files still missing a language | The same kind of search, without a key | Switch Podnapisi off in Settings → Subtitles |

**The fingerprint** is a 16-digit number computed from a video file's size and two small pieces of it (the first and last
64 KB). It lets a provider say "this subtitle is for exactly this file". The video itself is never sent. If you do not want
your files fingerprinted, switch both subtitle providers off.
| `discord.com`, `ntfy.sh` (or your own server), `api.telegram.org`, `api.pushover.net`, your own Gotify or webhook address, your own mail server | Alerts | **Only for channels you add**, when an event you ticked happens | A short message: the event and the title it is about, or what broke | Remove or switch off the channel in Settings → Connections → Alerts |

<!-- Subtitle rows: written from internal/subtitles (config, query fields); the provider code was not in the tree yet, so re-check the host names and the exact request fields (file name, size) when provider.go's implementations land. -->

Titles you monitor are refreshed from their provider every 12 hours, so TMDB, TVmaze and MusicBrainz are also
contacted about those, once each, twice a day.

**TVmaze, MusicBrainz and the cover archive have no switch** because shows and music cannot work without them, and none
of them needs an account. They are contacted only when you search for, add or refresh a show or an artist. If you
never use shows and music, they are never contacted. To block them anyway, block those hosts in your firewall:
Marquee reports "could not reach" for that kind of title and the rest keeps working.

### Your own apps

These are the addresses **you** enter, so Marquee only talks to the places you pointed it at.

| What | When | What is sent |
|---|---|---|
| **Download apps** (qBittorrent, Deluge, SABnzbd) | Every 5 seconds while something downloads, every minute otherwise; when it sends a release | The login you saved, the release to download, Marquee's category. |
| **Media servers** (Jellyfin, Emby, Plex) | After an import or a rename; when you press Test or "Open in..." | The API key or token, and the folder that changed. |
| **Sources** (Torznab, Newznab, or a Prowlarr or Jackett) | When it searches for a release, every 15 minutes for each source's new-release list, and when it fetches a `.torrent` or `.nzb` file | The words to search for (a title, a year, a season and episode, or the TMDB, TVmaze or IMDb ids), and the source's API key. A source is often a third party, so it learns what you look for. The `.torrent` or `.nzb` is fetched from the address the source gives. |

Whether those apps send things on to the world (a torrent app talks to peers) is up to them, not Marquee.

## What is stored, and where

Everything is in your config folder ([install](install.md#back-up-marqueekey)). Secrets such as API keys, tokens and
passwords for other apps are encrypted with the master key before they are written. Marquee's own passwords for people are
stored only as a salted, slow hash, and API keys as a hash, so neither can be read back. The access log
never records a query string or a request body, and the log line for a call from Claude records the tool and the
person, never what was asked.

Nothing in the config folder is sent anywhere. If you share logs when you ask for help, they contain titles and
your people's names (and, depending on the level, addresses of your own apps), so look before you paste.

## What the project itself sees

Nothing. The Docker image is pulled from GitHub's container registry, and the releases are downloaded from GitHub,
so GitHub sees those downloads like any other. Marquee, once running, never contacts the project.
