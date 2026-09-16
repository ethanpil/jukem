# jukem guide

This guide has the detail that is not in the readme: the paths, Docker, a
reverse proxy, backups, the configuration file and development.

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
| `/etc/jukem/config.yaml` | Bootstrap settings: listen address, data directory, log file, trusted proxies |
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

jukem serves plain HTTP only. For access away from home, use Tailscale or
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

## Upgrades

jukem tells you when a newer release exists. Settings > System shows the
version of the newest release, and the commands that install it. The
appliance asks GitHub once a day. It does not download the package and it
does not install it: a person does that. The switch **Check every day**
stops the daily request, for example on a network with no way out. The
button **Check now** always works.

To upgrade, install the new package with `apk add`, as the
[readme](../README.md) shows. The service starts again by itself.

**You can leave out releases.** An upgrade from 0.1.3 to 0.1.9 is the same
procedure as an upgrade from 0.1.8 to 0.1.9. jukem finds the schema version
of the database, applies each newer migration in order in its own
transaction, and stops at the first one that fails. Before the first
change it copies the database to `/var/lib/jukem/snapshots/`, so a rollback
to the old version is possible. The Health page shows the steps.

Do not go back to an older release while the database has a newer schema.
The old binary sees the newer schema version and refuses to start, because
it must not write data that the new schema needs.

## Configuration file

`/etc/jukem/config.yaml`:

```yaml
listen: ":80"
data_dir: /var/lib/jukem
log_file: /var/log/jukem/jukem.log
trusted_proxies: ["127.0.0.1/8", "::1"]
```

`JUKEM_LISTEN`, `JUKEM_DATA_DIR`, `JUKEM_LOG_FILE` and
`JUKEM_TRUSTED_PROXIES` (a comma list) override these. An empty `log_file`
logs to stdout. The Docker image uses that. In Docker, a proxy in another
container has an address on the Docker network, so add that network to
`trusted_proxies`.

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

### Rules for a schema change

A person can leave out releases, so each release must accept the database
of every older release.

- Add a new file `internal/store/migrations/000N_name.sql`. Give it the
  next number. The numbers must have no gap.
- Never change a migration that a release contains. It has already run on
  the machines of other people.
- SQLite cannot change a `CHECK` constraint. To make a constraint wider,
  build the new table, copy the rows, remove the old table and give the new
  table the old name. `0003_stream_source.sql` is an example.
- Keep the migration in one file: each file is one transaction, and a
  failure must leave a schema that still works.
- `TestMigrateFromEachOlderVersion` starts at every older schema and
  upgrades to the newest one. Keep it passing.
- Code that reads a row must accept a value that an older release wrote.
  `LoadSettings` puts the stored settings over the defaults, so a new
  setting has a value on an old database.

## Licence

MIT. See [LICENSE](LICENSE).

The logo and favicon are the "Music Library 2" icon from the Solar icon set
by 480 Design (Solar Bold Duotone Icons), under the Creative Commons
Attribution 4.0 licence: https://creativecommons.org/licenses/by/4.0/.
jukem changed only the colours for the light and dark themes.

The interface uses the Onest and IBM Plex Mono fonts, both under the SIL
Open Font License 1.1. The files are in `web/vendor/fonts/` with their
licences.
