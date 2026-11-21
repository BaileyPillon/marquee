# Marquee

One small self-hosted app for people who run their own movie, TV and music
library. It aims to do the jobs of Sonarr, Radarr, Lidarr, Bazarr, Prowlarr and
Seerr in a single static binary with the web interface built in, and to plug
into what you already run: qBittorrent, Deluge or SABnzbd to download, and
Jellyfin or Plex to watch.

**Status: pre-alpha (milestones M0 and M1).** What works today: the single
binary, the embedded UI shell, the health check, first-run owner setup,
sign-in with sessions, roles (owner, admin, member), invites, API keys with
permission scopes, and settings storage with encrypted secrets. Searching,
requests, downloading and importing are not built yet.

## Build

Needs Go 1.27 and Node (Node is only used to build the UI).

```powershell
.\scripts\build.ps1
```

That builds the UI, brotli-compresses it, and writes `bin\marquee.exe`,
`bin\marquee-linux-amd64`, and a Linux `.tar.gz` release archive, then prints a
size table. `.\scripts\build.ps1 -SkipUI` reuses the existing `web\dist`.
`.\scripts\dev.ps1` runs the server from source with its data in `.dev\config`.

## Run

```powershell
bin\marquee.exe
```

Then open <http://localhost:6464>. The first visitor becomes the owner.
On Linux, unpack the `.tar.gz` and run `./marquee`.

| Setting | Flag | Environment variable | Default |
|---|---|---|---|
| Listen address | `-addr` | `MARQUEE_ADDR` | `:6464` |
| Config folder | `-config` | `MARQUEE_CONFIG` | `./config` |
| Master key file | | `MARQUEE_MASTER_KEY_FILE` | `<config>/marquee.key` |
| Log level (`debug`, `info`, ...) | | `MARQUEE_LOG_LEVEL` | `info` |

The config folder holds `marquee.db`, `marquee-events.db` and `marquee.key`.
**Back up `marquee.key`**: it encrypts the API keys and passwords you save in
Settings, and they cannot be recovered without it.

## License and credits

MIT, see [LICENSE](LICENSE).

Inspired by HolyArrs by CoderLuii. Marquee is an independent clean-room
implementation and shares no code with it.
