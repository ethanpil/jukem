<img width="459" height="163" alt="image" src="https://github.com/user-attachments/assets/58ded212-45e4-43b8-92e2-7c2e57dba6dc" />

# JukeM

JukeM is a jukebox server designed for commercial spaces. It plays music 
on schedule through the sound hardware of its machine. Control JukeM via 
web page and/or included REST API. You can schedule local files or HTTP
streams, as well as timed announcements that pause the music.

## System requirements

- Alpine Linux 3.21 or later, on x86_64 or aarch64. Make the community
  repository available. The [guide](docs/guide.md) shows how to use Docker
  instead.
- A sound card, a USB DAC, or an HDMI output. (Pass through on VM or Docker)
- 256mb RAM, and 300mb Disk. (Plus the space for your library files)
- An accurate clock on the server for scheduling
- A web browser from 2023 or later, on a computer or on a telephone.

## Basic Architecture

The `jukem` package installs a binary that manages and monitors [mpd](https://www.musicpd.org) on
the server. JukeM monitors mpd to ensure everything is healthy. You control
the JukeM instance via its web interface.

JukeM is developed as a GoLang application, with help from LLMs.

## Installation

Do these steps as root. Replace `VERSION` with the number of the release.

```sh
VERSION=0.1.12
ARCH=$(apk --print-arch)
wget "https://github.com/ethanpil/jukem/releases/download/v$VERSION/jukem-$VERSION-$ARCH.apk"
apk add --allow-untrusted chrony "./jukem-$VERSION-$ARCH.apk"

rc-update add chronyd default
rc-update add jukem default
rc-service chronyd start
rc-service jukem start
```

`apk add` also installs MPD and the other programs that jukem needs.

Then open `http://<host>/` in a browser. The setup wizard asks for a
password, the time zone, the music folder and the sound output. It plays a
track at the end. Setup is complete when you hear that track.

jukem's configuration is stored in  `/etc/jukem/config.yaml`:

```yaml
listen: ":80"
data_dir: /var/lib/jukem
log_file: /var/log/jukem/jukem.log
trusted_proxies: ["127.0.0.1/8", "::1"]
```
These can be overridden at runtime for Docker or any other reason:

| Setting | ENV Variable | Description |
| listen | JUKEM_LISTEN | Port to listen on Web UI |
| data_dir | JUKEM_DATA_DIR | Storage of data files. (Container maps `/var/lib/` here) |
| log_file | JUKEM_LOG_FILE | Storage of log file | An empty `log_file` logs to stdout |
| trusted_proxies | JUKEM_TRUSTED_PROXIES | Comma separated list. |

For Docker its recommended to leave `log_file` empty so Docker can monitor the stdout logs.

If container has an address on the Docker network, so add that network to `trusted_proxies`.

## Upgrades

jukem checks for new releases on GitHub once per day. In the webui, 
`Settings > System` shows the version of the newest release.

To upgrade, install the new package with `apk add` just like first install.
The jukem service starts again by itself.

**You dont need incremental upgrades.** An upgrade from 0.1.3 to 0.1.9 is the 
same procedure as an upgrade from 0.1.8 to 0.1.9. jukem migrates schema during
an upgrade and before the first change it makes a copy of the database to 
`/var/lib/jukem/snapshots/`, so a rollback is possible. 

Do not go back to an older release while the database has a newer schema.
The old binary sees the newer schema version and refuses to start.

## Docker

TBD

### Music Library

Default location for the music library is `/srv/jukem/music`

The jukem service runs as the `jukem` user. Any audio files that you copy 
onto the machine as root, with scp, rsync or from a USB stick, belong to 
root. Give it to the service user:

```sh
chown -R jukem:jukem /srv/jukem/music
```

With incorrect permissions, web ui uploads and file operations in the 
library folders will fail. jukem shows the folders that are not writable
and the command to fix them in `Settings > Maintenance > Check library permissions`.

### Paths

| Path | Purpose |
|---|---|
| `/usr/bin/jukem` | The binary, with the web UI embedded |
| `/etc/init.d/jukem`, `/etc/conf.d/jukem` | OpenRC service script and options |
| `/etc/jukem/config.yaml` | Bootstrap settings: listen address, data directory, log file, trusted proxies |
| `/var/lib/jukem/` | Database, snapshots, MPD state, playlists |
| `/var/log/jukem/` | Log file, capped in size |
| `/srv/jukem/music/` | Default music root |

The database holds every other setting. Change them in the web UI.

## API

The REST API is under `/api/v1`. The OpenAPI document is at
`/api/v1/openapi.json` and the interactive docs at `/api/v1/docs`.

Create an API key in Settings > Security and send it as
`Authorization: Bearer <key>`. The web UI uses a session cookie instead. The
event stream at `/api/v1/events` says what changed; the client refetches the
resource.

## Forgotten password

On the console, as root:

```sh
jukem reset-password
```

This sets a new password and signs out every device.

## Access from outside the LAN

jukem serves plain HTTP only. For access away from LAN, use Tailscale or
WireGuard. Do not forward the port on the router.

For HTTPS, put a reverse proxy such as Caddy, nginx or Traefik in front of
jukem. Set up the proxy as follows:

- Send `X-Forwarded-For` and `X-Forwarded-Proto` on every request.
- Serve jukem at the root of a host name, not under a path such as
  `/jukem/`. The UI loads its files from `/app/` and `/api/v1/`.
- Allow request bodies as large as the upload limit in Settings > Library.
  nginx allows 1 MB by default, so set `client_max_body_size` there.
- Add the address of the proxy to `trusted_proxies` in the configuration
  file. The default trusts only a proxy on the same machine.

jukem reads the forwarding headers only from a trusted proxy. With them,
the login limit counts each client, and the session cookie is Secure over
HTTPS.

Use one address for jukem in each browser. A browser that signed in over
HTTPS keeps a Secure cookie. That browser cannot sign in over plain HTTP on
the same host name until the cookie expires or you clear the site data.


## Development

```sh
go test ./...
mkdir -p data && JUKEM_LISTEN=127.0.0.1:8080 JUKEM_DATA_DIR=./data JUKEM_LOG_FILE= go run ./cmd/jukem serve
scripts/package.sh amd64 v1.0.0      # unsigned apk in dist/
```

The web UI is plain ES modules under `web/app`, embedded in the binary.
Restart the server to see a change. `tools/apksign` signs the package the
way abuild does, with RSA-SHA256, so apk accepts it without
`--allow-untrusted`.

## More
- [Changelog](CHANGELOG.md)

## Music licensing (FYI)

A business that plays recorded music usually needs a public performance licence.

## Licence

MIT. See [LICENSE](LICENSE).

The logo and favicon are the "Music Library 2" icon from the Solar icon set
by 480 Design (Solar Bold Duotone Icons), under the Creative Commons
Attribution 4.0 licence: https://creativecommons.org/licenses/by/4.0/.
jukem changed only the colours for the light and dark themes.

The interface uses the Onest and IBM Plex Mono fonts, both under the SIL
Open Font License 1.1. The files are in `web/vendor/fonts/` with their
licences.
