# Reverse proxy and HTTPS

Marquee speaks plain HTTP. To reach it from outside your home, or to use a name like `marquee.example.com`
with a padlock, put a reverse proxy in front of it. Do that before you let anyone sign in from the internet: a
password sent over plain HTTP can be read by anyone on the way.

What Marquee needs from a proxy is small:

- It lives at the **root** of a host name (`https://marquee.example.com/`). Sub-paths such as `/marquee/` are not
  supported; use a sub-domain.
- **Live updates** (`/api/v1/events`) are a stream that stays open for hours. The proxy must not buffer it and
  must not close it after a minute.
- Pass the `Host` header and add `X-Forwarded-For` and `X-Forwarded-Proto`, and then tell Marquee which proxy
  to believe: see [trusted proxies](#trusted-proxies-and-the-visitors-address).
- Keep Marquee itself off the internet. When the proxy runs on the same machine, tell Marquee to listen on this
  machine only: `-addr 127.0.0.1:6464` (or `MARQUEE_ADDR=127.0.0.1:6464`). In a compose file, leave out the
  `ports:` line and let the proxy reach it by service name (`marquee:6464`).

## Caddy

Caddy gets and renews a certificate by itself when the name points at your server and ports 80 and 443 are open.
`Caddyfile`:

```caddyfile
marquee.example.com {
    reverse_proxy 127.0.0.1:6464 {
        flush_interval -1          # send live updates straight away
    }
}
```

Caddy passes `Host`, sets `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host` on its own, and does not
time out a stream. In Docker, use the service name instead: `reverse_proxy marquee:6464`.

## nginx

```nginx
server {
    listen 80;
    server_name marquee.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    http2 on;                      # nginx 1.25.1 or newer; on older versions write "listen 443 ssl http2;"
    server_name marquee.example.com;

    ssl_certificate     /etc/letsencrypt/live/marquee.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/marquee.example.com/privkey.pem;

    proxy_http_version 1.1;
    proxy_set_header Host              $host;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # The live-update stream: no buffering, and keep it open.
    location = /api/v1/events {
        proxy_pass http://127.0.0.1:6464;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 24h;
    }

    location / {
        proxy_pass http://127.0.0.1:6464;
    }
}
```

A certificate from [certbot](https://certbot.eff.org) or your DNS provider goes where `ssl_certificate` points.

## Trusted proxies and the visitor's address

Behind a proxy, every request reaches Marquee from the proxy's address. The real visitor, and whether they used
HTTPS, can only come from the headers the proxy adds, `X-Forwarded-For` and `X-Forwarded-Proto`. Anybody can
send those headers too, so **Marquee believes them only on a request that comes from a proxy you named**, and
ignores them on every other request, whatever they say.

Name your proxy in one of two ways (they add up):

- **Environment variable** `MARQUEE_TRUSTED_PROXIES`: addresses or CIDR ranges separated by commas or spaces,
  for example `MARQUEE_TRUSTED_PROXIES=127.0.0.1` or `MARQUEE_TRUSTED_PROXIES="172.18.0.0/16"`. A value that is not
  an address stops Marquee from starting, so a typo cannot quietly trust nobody.
- **The `server` setting** (the owner can change it with the API; it takes effect within a few seconds):

  ```sh
  curl -X PUT https://marquee.example.com/api/v1/settings/server \
    -H "Authorization: Bearer <an owner key with the settings permission>" \
    -H "Content-Type: application/json" \
    -d '{"publicUrl": "https://marquee.example.com", "trustedProxies": ["127.0.0.1", "172.18.0.0/16"]}'
  ```

Which address is "the proxy"? On one machine, usually `127.0.0.1`. In Docker it is the proxy container's address on
the network it shares with Marquee: find the network's range with `docker network inspect <network>`, and
trust that range (`172.16.0.0/12` covers Docker's defaults). A range that covers every address (`/0`) is refused.

**With a trusted proxy set**, Marquee uses the real visitor's address for its logs, the sign-in limit and the session
list, marks the sign-in cookie `Secure` when the proxy says the visit was HTTPS, and shows the `https://`
address on the Claude screen. For `X-Forwarded-For`, it takes the nearest address that is not itself a trusted
proxy, so a visitor cannot pick their own address by sending the header: your proxy has to add the real one
(Caddy and the nginx example above do).

**Without it**, the forwarded headers are ignored: logs and the sign-in limit see only the proxy's address (ten wrong
tries for one name from "the proxy" pause that name for a few minutes, for everyone), and the sign-in cookie is not
marked `Secure`. It still works, but a browser would also send it over plain HTTP if someone visited the `http://` address.

**`publicUrl`** is optional. It is the address people use from outside, `https://marquee.example.com`. When set,
Marquee shows it, instead of the `Host` header of the request, as the address for Claude and anywhere else it has to
write a full link. It is worth setting if your proxy rewrites `Host`.

Either way, redirect HTTP to HTTPS (the `return 301` above does), and, if you like, add an HSTS header
(`add_header Strict-Transport-Security "max-age=31536000" always;`). Marquee already sends a strict content
security policy with every page, and the sign-in cookie is `HttpOnly` and `SameSite=Lax`.

## Using the proxy with Claude

Claude Code sends a header `Authorization: Bearer mq_...` to `/mcp`. Two things to get right:

- If your proxy asks for its **own** login (basic auth, Authelia, Cloudflare Access, and so on), exempt `/mcp`
  and `/api/` from it, or give Claude's requests a way through. Otherwise the proxy answers before Marquee sees the key.
  Marquee itself ignores an `Authorization` header that is not a `Bearer mq_` key, so a proxy that adds its own
  does no harm.
- Allow request bodies of at least 1 MB (nginx's default is 1 MB already).

Then connect as in [Claude and MCP](claude-mcp.md): `claude mcp add --transport http marquee https://marquee.example.com/mcp --header "Authorization: Bearer <key>"`.

## Checking it

```sh
curl -i https://marquee.example.com/api/v1/health
```

should answer `200` with `{"status":"ok",...}`. If the page loads but live updates and download progress never
move, the proxy is buffering the stream: check the `/api/v1/events` settings above.
