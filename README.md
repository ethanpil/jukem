# jukem

jukem is a self-hosted jukebox appliance. It plays music on a weekly
schedule through the sound hardware of its machine. A web UI and a REST API
control it. MPD does the playback. jukem owns MPD: it writes the MPD
configuration, starts MPD as a child process, and restarts it when it
stops.

jukem comes as a signed Alpine apk for `x86_64` and `aarch64`, and as a Docker
image that installs that same package.

## What it does

- Plays a folder or a playlist during weekly windows, with date exceptions
  for holidays and one-off events.
- Lets a person press play, pause or stop at any time. The schedule resumes
  at the next window, or when the person presses Resume schedule.
- Fades in and out at window boundaries. Applies a volume floor and ceiling
  to people and API clients.
- Accepts uploads from the browser, moves and renames files, and keeps
  playlists and schedules pointing at the moved files.
- Watches for silence when music should play, a missing output device, a
  full disk, and an unstable MPD. It shows alerts in the UI and can post them
  to a webhook (generic JSON or ntfy).
- Keeps a play history.
- Runs a first-run wizard that ends when you confirm that you hear music.

See [PLAN.md](PLAN.md) for the design notes and the reasons behind them.

## Install on Alpine Linux

Use Alpine's `sys` install mode. The diskless and data-disk modes rebuild
the root filesystem at every boot from the package cache. A package from a
local file is not in that cache, so it is gone after the reboot. Support for
those modes needs a local apk repository and is not part of version 1.

Enable the community repository: remove the `#` before the `community` line
in `/etc/apk/repositories`. Then run, as root:

```sh
VERSION=1.0.0
ARCH=$(apk --print-arch)
BASE=https://github.com/OWNER/jukem/releases/download/v$VERSION

wget -qO /etc/apk/keys/jukem.rsa.pub "$BASE/jukem.rsa.pub"   # once; the key does not change
sha256sum /etc/apk/keys/jukem.rsa.pub                              # compare with SHA256SUMS on the release page
wget "$BASE/jukem-$VERSION-$ARCH.apk"
apk add chrony "./jukem-$VERSION-$ARCH.apk"

rc-update add chronyd default
rc-update add jukem default
rc-service chronyd start
rc-service jukem start
```

A pre-release tag such as `v1.3.0-rc1` gives the file name
`jukem-1.3.0_rc1-<arch>.apk`, because apk writes the suffix with an
underscore.

Open `http://<host>/` and follow the setup wizard. `apk add` installs `mpd`
and the other dependencies from Alpine's mirrors. chrony keeps the clock
correct, which the schedule needs.

To upgrade, download the new package and run `apk add` again. The service
restarts itself. Set `JUKEM_RESTART_ON_UPGRADE="no"` in `/etc/conf.d/jukem`
to restart at a time of your choice.

### Music copied by hand

The service runs as the `jukem` user. Music that you copy onto the machine
as root, with scp, rsync or from a USB stick, belongs to root. Give it to
the service user:

```sh
chown -R jukem:jukem /srv/jukem/music
```

Without this, uploads and file operations in those folders fail. jukem shows
the folders that are not writable and the command to fix them in Settings >
Maintenance > Check library permissions.

### Paths

| Path | Purpose |
|---|---|
| `/usr/bin/jukem` | The binary, with the web UI embedded |
| `/etc/init.d/jukem`, `/etc/conf.d/jukem` | OpenRC service script and options |
| `/etc/jukem/config.yaml` | Bootstrap settings: listen addresses, data directory, log file |
| `/var/lib/jukem/` | Database, snapshots, MPD state, playlists |
| `/var/log/jukem/` | Log file, capped in size |
| `/srv/jukem/music/` | Default music root |

The database holds every other setting. Change them in the web UI.

## Install with Docker

Prepare the two host directories. The image runs as UID and GID 1000, so the
directories must belong to that user:

```sh
mkdir -p /srv/jukem/music /srv/jukem/data
chown -R 1000:1000 /srv/jukem
```

Copy [compose.yaml](compose.yaml), set the host audio group id in
`group_add` (`getent group audio | cut -d: -f3`), and start it:

```sh
docker compose up -d
```

Open `http://<host>/`. If the music belongs to a different user, add
`user: "<uid>:<gid>"` to the service and give `/srv/jukem/data` the same
owner.

## First run

The wizard asks for a password, the time zone, the music root, the output
device and a first schedule. Then it plays the library and asks you if you
hear it. Setup is complete when you say yes. An empty library lets you
finish without the test.

If you hear nothing:

- Check that the correct output device is selected.
- Open Settings > Audio > Hardware level. A muted ALSA control is a common
  cause of silence.
- Check the amplifier and its volume.

## Health and alerts

The Health page answers one question in plain language: does it work? It
shows MPD, the scheduler, the clock, the output device, the library, the
storage and the last track change. A line that is not OK says what is wrong
and what to do.

Alerts show as a banner on every screen. To send them somewhere else, set a
webhook URL in Settings > System. The generic format posts JSON. The ntfy
format posts to an ntfy topic URL with a title and a priority.

`/healthz` answers `200` when the service works and `503` when it does not.
Docker uses it as the health check.

## Access from outside the home

jukem serves plain HTTP on the LAN. For access away from home, use Tailscale
or WireGuard. Do not forward the port on the router.

jukem serves HTTP only. For HTTPS, put a reverse proxy such as Caddy,
nginx or Traefik in front of it. Make the proxy send `X-Forwarded-Proto:
https`, so the session cookie is marked Secure. The proxy must not buffer
responses on `/api/v1/events`, because that is a live event stream.

## API

The REST API is under `/api/v1`. The OpenAPI document is at
`/api/v1/openapi.json` and the interactive docs at `/api/v1/docs`.

Create an API key in Settings > Security and send it as
`Authorization: Bearer <key>`. The web UI uses a session cookie instead. The
event stream at `/api/v1/events` says what changed; the client refetches the
resource.

## Backup

Stop the service and copy `/var/lib/jukem` (the `data` bind mount in
Docker). That directory is the complete backup. The music root is separate.

jukem copies the database to `/var/lib/jukem/snapshots/` before every schema
migration and when you press Database snapshot in Settings > Maintenance.
The Health page shows the rollback steps when a migration fails.

## Configuration file

`/etc/jukem/config.yaml`:

```yaml
listen: ":80"
data_dir: /var/lib/jukem
log_file: /var/log/jukem/jukem.log
```

`JUKEM_LISTEN`, `JUKEM_DATA_DIR` and `JUKEM_LOG_FILE` override these. An empty `log_file` logs to stdout, which the Docker image
uses.

## Forgotten password

On the console, as root:

```sh
jukem reset-password
```

This sets a new password and signs out every device.

## Music licensing

A business that plays recorded music usually needs a public performance
licence. In the UK that is PRS and PPL. In the US that is ASCAP, BMI, SESAC
and GMR. Music with a licence for commercial background use is the
exception. The play history helps when you must report what played.

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

## Licence

MIT. See [LICENSE](LICENSE).

The logo and favicon are the music library icon from the Solar Bold Duotone
Icons collection, used under the CC Attribution License. The dark-mode
version changes only the colour.
