# Connections

Marquee does not download or play anything itself. It connects to the apps you already run. All of this is in
**Settings → Connections** (and **Title info** for TMDB), and only the owner can change it. Every connection
has a **Test** button; use it after saving.

Secrets you type here (passwords, API keys, tokens) are encrypted on disk with your
[master key](install.md#back-up-marqueekey) and shown masked afterwards.

On Docker, `localhost` means the container itself. Use the other app's service name
(`http://qbittorrent:8080`) when both are in one compose file, or the host's address otherwise.

## Folders and hardlinks

First tell Marquee where your library is: **Settings → Library folders**. Add one folder per kind of
thing (movies, shows, music), using the full path as Marquee sees it (`D:\Movies`, `/data/movies`). A second
movie folder with `4k` or `uhd` in its name (for example `/data/movies-4k`) is where 4K copies go;
[requests](requests.md#4k-is-a-separate-copy) explains why. Marquee scans folders every 6 hours and reads what it finds
by file name. It never deletes a file unless you ask it to.

**Hardlinks.** When a torrent finishes, Marquee puts the file in your library as a *hardlink*: a second name for
the same data, so the file shows up in your library and keeps seeding, using the disk space once. A hardlink only
works when both names are on one filesystem, and, in Docker, only when both are reached through **one mount**.
If the link fails, Marquee falls back to copying the file (it works, but it needs twice the disk space).
Usenet downloads are moved instead, not linked.

The easy way to get this right:

```
/srv/media           one folder on one disk (the host)
  downloads/         the download app saves here
  movies/  movies-4k/  tv/  music/    your library folders
```

and mount `/srv/media` at the **same path** (say `/data`) in Marquee, in the download app and in the media
server. Then nothing needs translating (see [path rules](#path-rules) below). The
[compose file](../deploy/compose.yaml) is laid out like this.

## Download apps

Add one or more. When Marquee grabs a release it picks an enabled app that fits (a torrent app for torrents, a
Usenet app for Usenet), lowest priority number first.

| App | Address | Sign-in |
|---|---|---|
| **qBittorrent** (4.x and 5.x) | The web interface, for example `http://192.168.1.10:8080` | The web UI username and password. Leave them empty if qBittorrent skips the login for your machine. |
| **Deluge** (2.x) | The **web UI**, for example `http://192.168.1.10:8112` (not the daemon port) | The web UI password. The web UI must be connected to a daemon; Marquee connects it when it can. |
| **SABnzbd** | For example `http://192.168.1.10:8080` | The API key (SABnzbd: Config, General). |

**Category.** Marquee puts its downloads under a category (default `marquee`; you can change it per app). That is
how it tells them from your own, and how your other downloads are left alone. qBittorrent and SABnzbd have real
categories (Marquee creates it for you). Deluge uses its Label plugin when that is turned on; without the
plugin Marquee follows its own downloads by their ids.

**Save folder** is optional. Leave it empty to use the app's own download folder.

Marquee follows the downloads it sent. It checks the app every 5 seconds while something is downloading,
and when a download is finished it imports it (checks the files are really the title, unpacks rar, 7z and zip
when that is all there is, renames, links or moves it into the library folder, then tells your media server).
A release that turns out fake or wrong is added to the blocklist and Marquee looks for the next best one.

### Path rules

Only needed when an app sees the same file at a **different path** than Marquee does (typical with Docker
containers that mount folders at different places). A rule says "a path that starts with *From* starts with
*To* instead".

- **Media servers** (in the form: *Marquee's path* then *The media server's path*): for example, Marquee sees
  `/data/movies` and Jellyfin sees `/media/movies`, so the rule is from `/data/movies` to `/media/movies`.
- **Download apps** go the other way: from the path *as the app sees it* to the path *as Marquee sees it*:
  from `/downloads` to `/data/downloads`. The Connections screen does not show these yet; set them through the
  API with the `pathMap` field of `PUT /api/v1/clients/{id}` ([API](api.md)), or avoid the need by mounting the
  folders at the same path.

Paths can be written with `/` or `\`, and Windows drive letters ignore case.

## Media servers

Marquee tells your server to rescan after an import, and can show an **Open in Jellyfin** (or Plex, or Emby) button
on a title. It never plays anything, and it never needs more than a way to ask for a refresh.

| Server | Address | What to paste |
|---|---|---|
| **Jellyfin** | `http://192.168.1.10:8096` | An API key: Jellyfin dashboard, **API Keys**, new key. |
| **Emby** | `http://192.168.1.10:8096` | An API key: Emby settings, **Advanced**, **API Keys**. |
| **Plex** | `http://192.168.1.10:32400` | A Plex token (`X-Plex-Token`) for the account that owns the server. Plex's support site explains how to find it. |

Add a path rule if the server sees your library at a different path. After saving, **Test** shows the server's
version and its libraries. A server that is switched off or unreachable never stops an import: the problem is
shown on the server's row and Marquee tries again next time.

## Sources

Sources are where Marquee looks for releases. **Marquee has no built-in search of public sites.** You bring the
feeds your own indexers give you:

- **Torznab** (torrents) or **Newznab** (Usenet): an address (the feed URL) and its API key. You can paste a URL
  that already has `?apikey=...` on the end; Marquee takes the key out and stores it encrypted.
- **Import from Prowlarr or Jackett** (the button on the Sources card): give the address of your Prowlarr or
  Jackett and its API key, and Marquee adds one source for each of its usable indexers. It is safe to run
  again; indexers already added are skipped.
- **Categories** (optional): numbers such as 2000 (movies), 5000 (TV), 3000 (audio). Left empty, Marquee asks
  for the main category of whatever it is searching for. Setting them limits a source to those kinds.
- **Priority**: a lower number is asked first (1 to 50; the default is 25).

Marquee asks every enabled source at the same time, parses and ranks what comes back with the
[quality rules](configuration.md#what-you-set-in-the-web-page), and tells you in plain words why each one was
picked or turned down. It also reads each source's RSS list every 15 minutes for new releases.

## TMDB

Movies come from [The Movie Database](https://www.themoviedb.org). Shows (TVmaze) and music (MusicBrainz) need no
key, but **movies need yours**. A free TMDB account gives you one: in the TMDB site, **Settings → API**. Either of
the two credentials works: the short **API Key** (v3) or the long **API Read Access Token** (v4).

Paste it in **Settings → Title info → TMDB**. It takes effect at once, and until then movie search says so
rather than failing. See [privacy](privacy.md) for what is sent.

## Subtitles

Marquee fetches subtitles for the movies and episodes in your library and saves them next to the video, named
`<video name>.<language>.srt` (`.hi.srt` for the hearing impaired). It never overwrites a subtitle file it did not
write itself. Set everything in **Settings → Subtitles**:

- **Languages**: the ones you want, in order (up to 12). The default is English.
- **Providers**: **OpenSubtitles.com** with your own API key (a free OpenSubtitles account has one; add your
  username and password too for a bigger daily download quota), and **Podnapisi**, which needs no key. Podnapisi
  is **on by default**, so a new Marquee looks for English subtitles after each import until you change that; see
  [privacy](privacy.md).
- **Hearing impaired**: prefer not, prefer, or only.
- **Minimum score** (0 to 100, default 60): what an automatic download must reach. Marquee scores each result against
  your video, first by the exact file (a hash of it, so a match is certain), then by the release name (group, source
  and resolution). The best result that clears the minimum is saved.
- **When it looks**: right after an import, and on a schedule for files still missing a language, waiting 1, then 3,
  then 7 days between tries for one file. You can also press **Find subtitles** on a file.

## Plex watchlists

**Settings → Connections → Watchlists → Link Plex** starts the Plex sign-in: Marquee shows a short code and a
link to plex.tv; you approve it there. After that, every 15 minutes Marquee reads your Plex watchlist (and,
if you allow it, your Plex friends' watchlists) and turns what it finds into asks. See [requests](requests.md#watchlists).

## Alerts

Channels tell you about things that happen. Add as many as you like, each with the events it wants:

- **Discord** (a webhook address), **ntfy** (a topic; the server is `https://ntfy.sh` unless you run your own),
  **Gotify** (server and token), **Telegram** (bot token and chat id), **Pushover** (app token and user key),
  **email** (an SMTP server, port, security, login, from and to) and a plain **webhook** (an address and an
  optional signing secret).
- Events: a new ask, an ask approved, an ask declined, a title became available, a download failed, an import
  failed, and "something needs a look" (a connection broke). The same event is not sent twice to a channel within
  10 minutes, and a channel gets at most 30 messages a minute.

Use **Send a test message** to check a channel. Webhook addresses and passwords are stored encrypted.
