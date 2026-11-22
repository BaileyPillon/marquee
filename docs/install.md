# Install

Marquee is one program. Pick the way that fits your machine:

- [Windows](#windows)
- [Linux](#linux)
- [Docker](#docker)
- [macOS](#macos-apple-silicon)

Whichever you choose, the first run is the same: [open the page and become the owner](#first-run).

## Windows

1. Download `marquee_<version>_windows_amd64.zip` from the
   [Releases page](https://github.com/BaileyPillon/marquee/releases) and unzip it somewhere permanent,
   for example `C:\Marquee`.
2. Run `marquee.exe`. Windows will ask whether to let it use the network. Allow **private networks** if
   you want phones and other computers at home to reach it.
3. Open <http://localhost:6464>.

Marquee keeps its files in a `config` folder. By default that is `.\config` in whatever folder Marquee was
started from, so tell it where you want it:

```powershell
.\marquee.exe -config D:\Marquee\config
```

Unsigned programs make SmartScreen say "Windows protected your PC". Choose **More info**, then
**Run anyway**. To check the download first, compare its hash with the one in `SHA256SUMS` on the release page:

```powershell
Get-FileHash .\marquee_0.1.0_windows_amd64.zip -Algorithm SHA256
```

To start Marquee when you sign in, make a Task Scheduler task with the trigger "At log on", the action
"Start a program" pointing at `marquee.exe` (with `-config ...` as the arguments) and "Start in" set to its folder.

To stop other devices from connecting, listen on this computer only: `.\marquee.exe -addr 127.0.0.1:6464`.

## Linux

Archives are published for `amd64`, `arm64` and `armv7` (32-bit Raspberry Pi OS and similar).

```sh
# pick your architecture: amd64, arm64 or armv7
curl -LO https://github.com/BaileyPillon/marquee/releases/download/v0.1.0/marquee_0.1.0_linux_amd64.tar.gz
curl -LO https://github.com/BaileyPillon/marquee/releases/download/v0.1.0/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS

sudo mkdir -p /opt/marquee && sudo tar -xzf marquee_0.1.0_linux_amd64.tar.gz -C /opt/marquee
/opt/marquee/marquee -config /var/lib/marquee
```

To keep it running, use a systemd service. Make a user for it first and give that user read and write access
to your media and download folders (see [hardlinks](connections.md#folders-and-hardlinks)):

```sh
sudo useradd --system --home /var/lib/marquee --create-home --shell /usr/sbin/nologin marquee
```

`/etc/systemd/system/marquee.service`:

```ini
[Unit]
Description=Marquee
After=network-online.target
Wants=network-online.target

[Service]
User=marquee
Group=marquee
ExecStart=/opt/marquee/marquee
Environment=MARQUEE_CONFIG=/var/lib/marquee
Environment=MARQUEE_ADDR=:6464
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now marquee
journalctl -u marquee -f        # watch the log
```

## Docker

The image is built `FROM scratch`: the Marquee binary, CA certificates and the time zone database, nothing else.
It runs for `linux/amd64`, `linux/arm64` and `linux/arm/v7`.

```sh
docker run -d --name marquee --restart unless-stopped \
  -p 6464:6464 \
  -v marquee-config:/config \
  -v /srv/media:/data \
  -e TZ=America/New_York \
  ghcr.io/baileypillon/marquee:latest
```

Or use Docker Compose. [`deploy/compose.yaml`](../deploy/compose.yaml) is a ready file with comments and
optional qBittorrent and Jellyfin services:

```sh
docker compose -f deploy/compose.yaml up -d
```

**What to know about the image**

- **One media volume.** Mount your media and your downloads as one host folder at one path (`/data` above),
  and use sub-folders of it. Hardlinks cannot cross from one mount to another. See
  [folders and hardlinks](connections.md#folders-and-hardlinks).
- **The default user is 65532.** The image has no shell, so the usual `PUID` and `PGID` variables are **not
  supported**. A named volume for `/config` works as it is, because the image's `/config` already belongs to
  65532. But if your media folders belong to someone else (usually they do), run Marquee as that user with
  Docker's own option, and keep `/config` in a folder that user owns:

  ```yaml
  services:
    marquee:
      user: "1000:1000"            # your ids: run `id -u` and `id -g`
      volumes:
        - ./config:/config         # mkdir config && chown 1000:1000 config
        - /srv/media:/data
  ```

  The same thing on the command line is `docker run --user 1000:1000 ...`.
- **Settings through environment.** The image sets `MARQUEE_CONFIG=/config` and `MARQUEE_ADDR=:6464`. See
  [configuration](configuration.md) for the rest.
- **Health check.** There is no shell or `curl` in the image, so the check is a command built into the binary:
  `marquee healthcheck`. The image runs it every 30 seconds (20 seconds of grace at start, 3 misses in a row
  before it says unhealthy), so `docker ps` shows `(healthy)` or `(unhealthy)`. It asks
  `http://127.0.0.1:6464/api/v1/health` from inside the container and passes only on HTTP 200 with
  `"status":"ok"`; otherwise it exits 1 with one line saying why. It reads the port from `MARQUEE_ADDR` (or
  `-addr host:port`), so it follows you if you change the port inside the container. Try it by hand with
  `docker exec marquee /marquee healthcheck`. It never reads your config folder or keys. Outside Docker,
  `http://<host>:6464/api/v1/health` answers with the same JSON and is easy to watch from outside (for example
  with Uptime Kuma). To turn the image's check off, set `healthcheck: { disable: true }` in compose or
  `--no-healthcheck` on `docker run`.
- **Pin a version** (`ghcr.io/baileypillon/marquee:0.1.0`) once you are happy, so an update is a choice.

To build the image yourself, from the repository root:

```sh
docker build -f deploy/Dockerfile --build-arg VERSION=$(cat VERSION) -t marquee .
```

## macOS (Apple silicon)

The release has a `darwin_arm64` archive. It is built by the release workflow but has not been tested on a Mac
yet. The program is unsigned, so remove the quarantine flag once after unpacking:

```sh
xattr -d com.apple.quarantine ./marquee
./marquee -config ~/Library/Application\ Support/Marquee
```

## First run

Open `http://<this machine>:6464`. A new Marquee has no people yet, and **the first visitor becomes the owner**.
So open it yourself straight away, before you share the address or open the port to the internet.

1. Pick a name and a password of at least 8 characters.
2. You land on Home. Open **Settings** and, in this order:
   - **Library folders**: add your movie, TV and music folders ([connections](connections.md#folders-and-hardlinks)).
   - **Title info**: paste your TMDB key if you want movies ([connections](connections.md#tmdb)).
   - **Connections**: add a download app, a media server and, if you have them, sources.
   - **People & limits** and invites when you are ready to share. The owner makes one-time invite links;
     see [requests](requests.md).
3. Search for something and ask for it.

If you ever lose the owner's password there is no reset page, because there is no email. Keep it in a
password manager.

## Back up `marquee.key`

Everything you type into Settings that is a secret (API keys, tokens, passwords for your other apps, webhook
addresses) is encrypted before it is written to the database, using a random key stored in
`marquee.key` in the config folder. **If you lose that file, those secrets cannot be recovered**, and you will
have to type them all in again. The rest (people, titles, asks, history) is not encrypted and still works.

What to back up:

| File in the config folder | What it is | Needed? |
|---|---|---|
| `marquee.key` | the master key for secrets | **yes**, keep a copy somewhere safe and separate |
| `marquee.db` | people, titles, asks, settings, sources, downloads | yes |
| `marquee-events.db` | history and logs | nice to have |
| `cache/` | cached artwork | no, it is rebuilt |

The database runs in WAL mode, so the safest backup is to stop Marquee for a moment and copy the whole
`config` folder. Keeping `marquee.key` in the same backup as `marquee.db` is convenient, but a thief with both can
read your secrets; if that matters to you, store them apart. You can move the key to another place with
`MARQUEE_MASTER_KEY_FILE`.

## Updating

- **Windows and Linux:** stop Marquee, replace the program with the new one, start it.
- **Docker:** `docker compose pull && docker compose up -d` (or pull the new tag and recreate the container).

The database is upgraded by the program when it starts. Take a backup first, and do not run an older
version against a newer database.
