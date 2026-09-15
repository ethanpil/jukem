# jukem: Design Notes

jukem is a self-hosted jukebox appliance. MPD plays music through the sound hardware of the machine jukem is installed on, and a web UI and REST API control it. It ships as a signed Alpine apk for x86_64 and arm64, and as a Docker image built from that same package.

The project, GitHub repository, Go module, binary, OpenRC service, and system user are all named `jukem`. `OWNER` below stands for the GitHub user or organization that hosts the repository.

The target is one appliance on a trusted LAN, playing background music on a schedule, set up by the person who owns it. Everything below is sized for that: one login, one output, one music library, and no infrastructure the product doesn't need.

## Architecture

```text
  Browser / phone          Remote app (later)
         |                        |
         +----- HTTP: REST + SSE -+
                     |
  +------------------v-------------------+
  | jukem (single Go binary)           |
  |                                      |
  |  API + auth        Web UI (embedded) |
  |  Scheduler         Device manager    |
  |  Library/uploads   Watchdog          |
  |  SQLite            MPD supervisor    |
  +------------------+-------------------+
                     | unix socket only
  +------------------v-------------------+
  | MPD (child process of jukem)       |
  +--------+-------------------+---------+
           | ALSA              | reads
           v                   v
  USB DAC / HAT / HDMI     music root
           |
        Speakers
```

Four ideas carry most of the design.

**jukem owns MPD.** It renders `mpd.conf`, starts `mpd --no-daemon` as a child process, and restarts it when it exits. Changing the output device or the music root rewrites the config and restarts MPD. Because the app owns that step, bare metal and Docker behave identically, and jukem never needs root, OpenRC access, or the Docker socket.

**The scheduler converges on a desired state.** It doesn't fire start and stop events. Every few seconds it works out what should be playing, compares that with what MPD is doing, and corrects the difference. After a reboot, a power cut, or an MPD crash, playback returns to the right state on its own.

**MPD stays off the network.** Its protocol is unencrypted and its password travels in plain text. MPD listens only on a unix socket inside jukem's data directory, and every client, the web UI included, goes through jukem's API.

**One package defines the install.** The apk declares the files, the system user, and the dependencies. Bare-metal installs use it directly, and the Docker image installs the same apk, so the two targets can't drift apart.

## Stack

| Layer | Choice | Reason |
|---|---|---|
| Backend | Go 1.27, `net/http` with huma v2 | A static binary with no libc dependency, so one build per architecture runs on every Alpine release. huma generates the OpenAPI spec from handler types. |
| MPD client | `github.com/fhs/gompd/v2/mpd` | Unix socket support and an idle-event watcher |
| Database | SQLite via `modernc.org/sqlite` | Pure Go, which keeps `CGO_ENABLED=0` |
| Web UI | Bootstrap 5.3.8, Bootstrap Icons, SortableJS, plain ES modules | No Node, bundler, or build step |
| Live updates | Server-Sent Events | Change notifications flow server to client; commands and state go over REST. Browsers reconnect on their own. |
| Packaging | nFPM, pinned as a Go tool in `go.mod` | Builds and signs apks for both architectures from one config file, on any machine, in seconds |

abuild, Alpine's own packager, builds for the architecture it runs on, so arm64 packages would need slow emulated Go compiles in CI. Go cross-compiles natively and nFPM wraps each binary in a signed apk. An APKBUILD only becomes necessary if jukem is ever submitted to Alpine's aports.

Go 1.24 added `tool` directives to `go.mod`. Running `go get -tool github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0` once pins nFPM next to the code's other dependencies, and scripts call it as `go tool nfpm`, so nobody installs it separately.

## Repository layout

```text
jukem/
  cmd/jukem/             main; subcommands serve, healthcheck, reset-password, version
  internal/
    api/                   handlers, auth middleware, OpenAPI
    audio/                 device discovery and identity, hardware mixer
    mpdctl/                supervisor, conf template, client pool, idle watcher
    player/                queue actions, command intent tracking
    scheduler/             rule expansion, resolver, reconciler, overrides, fades
    library/               browse, search, uploads, file operations, path checks
    store/                 SQLite access
    store/migrations/      numbered SQL migrations, embedded
    events/                SSE fan-out
    watchdog/              health checks and alerts
  web/
    embed.go               go:embed of the files below
    index.html             app shell
    app/                   ES modules: router, API client, views, upload manager
    vendor/                bootstrap, bootstrap-icons, sortablejs, VERSIONS
  packaging/
    nfpm.yaml
    config.yaml            installed as /etc/jukem/config.yaml
    openrc/jukem.initd
    openrc/jukem.confd
    scripts/               pre-install.sh, post-install.sh, post-upgrade.sh
    keys/jukem.rsa.pub   public signing key (the private key is never committed)
  scripts/package.sh       build the binary and apk for one architecture
  tools/apksign/           signs an apk with RSA-SHA256, the way abuild does
  Dockerfile
  .dockerignore
  compose.yaml
  .github/workflows/       ci.yml, release.yml
```

`web/embed.go` sits inside `web/` because `go:embed` can't reach parent directories. `reset-password` sets a new login password from a console without touching anything else.

## Supported hardware

| Hardware | Alpine arch | Go target | Docker platform |
|---|---|---|---|
| Intel and AMD 64-bit PCs, NUCs, thin clients | `x86_64` | `amd64` | `linux/amd64` |
| Raspberry Pi 3, 4, 5, Zero 2 W on 64-bit Alpine | `aarch64` | `arm64` | `linux/arm64` |

Two architectures, not four. 32-bit ARM doubles the CI matrix, the release artifacts, and the hardware to test on, and the machines that need it (Pi 1, Pi Zero, Pi 2) are poor hosts for SQLite, TLS, and browser uploads. `apk --print-arch` shows which package a machine needs; 32-bit builds are one line in `package.sh` if anyone ever asks.

## Packaging

### What the apk installs

| Path | Purpose |
|---|---|
| `/usr/bin/jukem` | The binary, with the web UI embedded |
| `/etc/init.d/jukem` | OpenRC service script |
| `/etc/conf.d/jukem` | Service options, preserved on upgrade if edited |
| `/etc/jukem/config.yaml` | Bootstrap settings, preserved on upgrade if edited |
| `/var/lib/jukem/` | Database, database snapshots, generated `mpd.conf`, MPD database, state file and socket, playlists, TLS material. Created by post-install, owned by `jukem`, mode 0750. |
| `/var/log/jukem/` | Size-capped log file |
| `/srv/jukem/music/` | Default music root, created only if missing |

The package depends on `mpd`, `alsa-utils`, `tzdata`, and `ca-certificates` from Alpine's own repositories. `mpd` lives in the community repository, which must be enabled.

Alpine packages don't enable their services on install, so `rc-update add jukem default` is the step that turns it on.

### `packaging/nfpm.yaml`

```yaml
name: jukem
arch: ${NFPM_ARCH}
version: ${VERSION}
release: 0
maintainer: jukem maintainers <jukem@example.com>
description: Jukebox appliance that plays MPD through the local sound hardware
homepage: https://github.com/OWNER/jukem
license: MIT
depends:
  - mpd
  - alsa-utils
  - tzdata
  - ca-certificates
contents:
  - src: build/jukem
    dst: /usr/bin/jukem
    file_info:
      mode: 0755
  - src: packaging/openrc/jukem.initd
    dst: /etc/init.d/jukem
    file_info:
      mode: 0755
  - src: packaging/openrc/jukem.confd
    dst: /etc/conf.d/jukem
    type: config|noreplace
  - src: packaging/config.yaml
    dst: /etc/jukem/config.yaml
    type: config|noreplace
scripts:
  preinstall: packaging/scripts/pre-install.sh
  postinstall: packaging/scripts/post-install.sh
apk:
  scripts:
    postupgrade: packaging/scripts/post-upgrade.sh
```

nFPM always produces an unsigned package here; signing is a separate step, for the reason given under Signing.

### Install scripts

`pre-install.sh` creates the system user and adds it to `audio`:

```sh
#!/bin/sh
addgroup -S jukem 2>/dev/null
adduser -S -D -H -h /var/lib/jukem -s /sbin/nologin -G jukem -g jukem jukem 2>/dev/null
addgroup jukem audio 2>/dev/null
exit 0
```

`post-install.sh` creates directories without touching any that already exist:

```sh
#!/bin/sh
[ -d /var/lib/jukem ] || install -d -o jukem -g jukem -m 0750 /var/lib/jukem
[ -d /var/log/jukem ] || install -d -o jukem -g jukem -m 0750 /var/log/jukem
[ -d /srv/jukem/music ] || install -d -o jukem -g jukem -m 0755 /srv/jukem/music
cat <<MSG
 *
 * jukem is installed. To start it now and at every boot:
 *   rc-update add jukem default
 *   rc-service jukem start
 * Then open http://<this-host>/ and follow the setup wizard.
 *
MSG
exit 0
```

`post-upgrade.sh` restarts the service so the new binary takes effect:

```sh
#!/bin/sh
# Restart the service so the new binary takes effect. Opt out in /etc/conf.d/jukem.
[ -f /etc/conf.d/jukem ] && . /etc/conf.d/jukem

case "${JUKEM_RESTART_ON_UPGRADE:-yes}" in
	[Yy]es|[Tt]rue|1) ;;
	*) echo " * jukem upgraded. Restart it with: rc-service jukem restart"; exit 0 ;;
esac

if command -v rc-service >/dev/null 2>&1 && rc-service jukem status >/dev/null 2>&1; then
	echo " * Restarting jukem"
	rc-service jukem restart
fi
exit 0
```

An appliance that needs a manual restart after every update isn't finished. The restart takes a second or two: MPD's state file holds the queue and position, and the reconciler resumes the scheduled program. Setting `JUKEM_RESTART_ON_UPGRADE="no"` in `/etc/conf.d/jukem` turns it off for anyone who'd rather pick the moment.

### OpenRC service

`packaging/openrc/jukem.initd`:

```sh
#!/sbin/openrc-run

name="jukem"
description="Jukebox appliance"
command="/usr/bin/jukem"
command_args="serve --config ${JUKEM_CONFIG:-/etc/jukem/config.yaml}"
command_user="jukem:jukem"
capabilities="^cap_net_bind_service"
supervisor="supervise-daemon"
respawn_delay=2
respawn_max=0

depend() {
	need localmount
	after net chronyd ntpd
}
```

`packaging/openrc/jukem.confd`:

```sh
# Configuration file for /etc/init.d/jukem

# Bootstrap config file.
#JUKEM_CONFIG="/etc/jukem/config.yaml"

# Restart the service automatically after "apk upgrade" installs a new version.
# Playback stops for a few seconds and then resumes on its own.
#JUKEM_RESTART_ON_UPGRADE="yes"
```

supervise-daemon restarts jukem if it exits, and `respawn_max=0` removes the retry limit. Switching to `command_user` also loads that user's supplementary groups, which is how the process reaches `/dev/snd`. The `capabilities` line grants the ambient `cap_net_bind_service`, so the unprivileged process can bind port 80 and people can type a bare hostname. `after net` rather than `need net` means music still starts when the network is down.

### Bootstrap config

`packaging/config.yaml`, installed as `/etc/jukem/config.yaml`:

```yaml
# Bootstrap settings. Everything else is configured in the web UI.
listen: ":80"
listen_tls: ":443"    # used when HTTPS is switched on in Settings > Security
data_dir: /var/lib/jukem
log_file: /var/log/jukem/jukem.log
```

`JUKEM_LISTEN`, `JUKEM_LISTEN_TLS`, `JUKEM_DATA_DIR`, and `JUKEM_LOG_FILE` override these. An empty `log_file` logs to stdout, which is what the Docker image uses. The music root is not here: it's chosen in the setup wizard and lives in the database, so there's only one place to change it. Unknown keys produce a warning and are ignored; missing keys fall back to these defaults.

### `scripts/package.sh`

```sh
#!/bin/sh
# Build the jukem binary and an .apk for one architecture.
# Usage:   scripts/package.sh <amd64|arm64> <version>
# Signing: set APK_SIGNING_KEY to the private key path; unset gives an unsigned dev build.
set -eu

arch="$1"
version="${2#v}"

case "$arch" in
	amd64) apkarch=x86_64 ;;
	arm64) apkarch=aarch64 ;;
	*) echo "unsupported arch: $arch (jukem builds amd64 and arm64)" >&2; exit 1 ;;
esac

mkdir -p build dist
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
	go build -trimpath -ldflags "-s -w -X main.version=$version" \
	-o build/jukem ./cmd/jukem

out="dist/jukem-$version-$apkarch.apk"
NFPM_ARCH="$arch" VERSION="$version" \
	go tool nfpm package --packager apk --config packaging/nfpm.yaml --target "$out"

if [ -n "${APK_SIGNING_KEY:-}" ]; then
	go run ./tools/apksign -key "$APK_SIGNING_KEY" -name jukem.rsa.pub "$out"
fi
```

The nFPM config, install scripts, OpenRC files, and this script were exercised with nFPM 2.47.0 with the Go build stubbed. Both packages came out with the correct architecture, `pkgver`, dependencies, install scripts, and file modes, and the upgrade-restart logic behaves correctly for an unset, `yes`, and `no` setting. The RSA-SHA256 signing step was prototyped separately (see Signing) and the resulting package verified against the public key with openssl. Installing on a live Alpine system and building the image stay untested until the Go code exists; CI covers that.

Git tags map to apk versions: `v1.2.0` becomes `1.2.0-r0` and `v1.3.0-rc1` becomes `1.3.0_rc1-r0`. apk accepts only certain suffixes (`alpha`, `beta`, `pre`, `rc`, `p`), so don't tag builds `-dev`.

### Signing

Generate the key once, on a trusted machine:

```sh
openssl genrsa -traditional -out jukem.rsa 4096
openssl rsa -in jukem.rsa -pubout -out jukem.rsa.pub
```

Commit `jukem.rsa.pub` to `packaging/keys/`, store the private key as the GitHub Actions secret `APK_SIGNING_KEY`, and keep an offline backup. `-traditional` writes the PKCS#1 PEM that Alpine's tools produce.

nFPM's own apk signing produces a `.SIGN.RSA` entry, which is an RSA signature over a SHA-1 digest, and it can't produce anything else. Alpine's tools moved to `.SIGN.RSA256` (SHA-256) some time ago, and apk-tools v3 may stop accepting the SHA-1 form. Rather than depend on that, jukem signs its own packages the way abuild does. An apk is three concatenated gzip streams: signature, control, data. `tools/apksign` takes the unsigned two-stream file nFPM produces, signs the control stream with RSA PKCS#1 v1.5 over SHA-256, and prepends a stream containing a single tar entry named `.SIGN.RSA256.jukem.rsa.pub` holding the signature. It's about sixty lines of Go and keeps the release pipeline free of anything but Go and openssl.

The format was prototyped: an unsigned nFPM package was split into its streams, signed this way, and reassembled; the entry name and the signature verified with `openssl dgst -sha256 -verify`. Whether apk itself accepts it is what the install-test job proves.

The entry name matters: apk looks for a public key with exactly that name, `jukem.rsa.pub`, in `/etc/apk/keys/`.

### Release pipeline

`release.yml` runs on a `v*` tag.

| Job | What it does |
|---|---|
| test | `go vet ./...` and `go test ./...` |
| packages | `scripts/package.sh` for amd64 and arm64, signed with `APK_SIGNING_KEY` through `tools/apksign` |
| install-test | For each package, on the oldest and newest supported Alpine release, starts `alpine:<release>`, adds the public key, runs `apk add` on the package without `--allow-untrusted`, and runs `jukem version`. Four combinations, which prove the signature, dependency resolution, and the binary. The arm64 half runs on GitHub's native `ubuntu-24.04-arm` runners, which are free for public repositories, so nothing is emulated. |
| release | Creates the GitHub Release with both apks, `jukem.rsa.pub`, and `SHA256SUMS` |
| image | `docker buildx build --platform linux/amd64,linux/arm64 --push`, tagged `ghcr.io/OWNER/jukem:<version>` and `:latest` |

`ci.yml` runs test, unsigned packages, and install-test on every pull request.

Releases are GitHub Releases plus a container image. There's no apk repository, no GitHub Pages site, and no index signing: that's a small Linux distribution's worth of infrastructure around an application that doesn't exist yet. Once the software has been stable through a few releases, the same signed packages can be laid out as a repository so `apk upgrade` picks them up, without changing anything here.

Alpine 3.23 moved to apk-tools v3 but kept the v2 package format that nFPM produces, and apk v3 reads v2 packages. The install-test job fails the first time that stops being true, or the first time a release rejects the SHA-256 signature form.

## Installation

### Alpine on bare metal or a VM

Enable the community repository (uncomment the `community` line in `/etc/apk/repositories`), then, as root:

```sh
VERSION=1.0.0
ARCH=$(apk --print-arch)
BASE=https://github.com/OWNER/jukem/releases/download/v$VERSION

wget -qO /etc/apk/keys/jukem.rsa.pub "$BASE/jukem.rsa.pub"   # once; the key never changes
sha256sum /etc/apk/keys/jukem.rsa.pub                              # compare with the README
wget "$BASE/jukem-$VERSION-$ARCH.apk"
apk add chrony "./jukem-$VERSION-$ARCH.apk"

rc-update add chronyd default
rc-update add jukem default
rc-service chronyd start
rc-service jukem start
```

Open `http://<host>/` and follow the setup wizard. `apk add` pulls `mpd` and the other dependencies from Alpine's mirrors.

Upgrading is one `wget` of the new package and `apk add`; the service restarts itself. The key is fetched once and never again, so nobody gets used to replacing the trust anchor from the network.

If you copy music onto the box as root, with scp, rsync, or from a USB stick, give it to the service user afterwards: `chown -R jukem:jukem /srv/jukem/music`. Otherwise uploads and file operations in those folders fail, and jukem will tell you so.

Use Alpine's `sys` install mode for the appliance. Diskless and data-disk modes rebuild the root filesystem at every boot from `/etc/apk/world` and the package cache, and a package installed from a local file isn't in any repository apk can resolve, so after `lbu commit` and a reboot the package is gone. Making that work needs a local repository: a directory on persistent storage holding the apk and an `APKINDEX.tar.gz` signed with a locally generated `abuild-keygen` key, added to `/etc/apk/repositories`. That's most of the deferred apk-repository work, so it isn't supported in v1 and the README says so.

### Docker

`compose.yaml`:

```yaml
services:
  jukem:
    image: ghcr.io/OWNER/jukem:latest
    init: true
    restart: unless-stopped
    ports:
      - "80:8080"
    group_add:
      - "29"                         # host audio GID: getent group audio | cut -d: -f3
    device_cgroup_rules:
      - "c 116:* rmw"                # ALSA devices, including USB DACs plugged in later
    volumes:
      - /dev/snd:/dev/snd
      - /srv/jukem/music:/srv/jukem/music
      - /srv/jukem/data:/var/lib/jukem
```

Prepare the two host directories, then `docker compose up -d` and open `http://<host>/`:

```sh
mkdir -p /srv/jukem/music /srv/jukem/data
chown -R 1000:1000 /srv/jukem
```

The image runs as UID and GID 1000, which is the first regular user on most hosts and therefore the most common owner of a music directory. Both the music and the data directory are bind mounts owned by that UID. A named volume for the data directory was the earlier design and it had a real bug: Docker initializes a named volume with the image's ownership, so any `user:` override that didn't match the image's UID couldn't write its own database. With a bind mount, the host directory's owner is the only thing that matters. If the music belongs to a different UID, add `user: "<uid>:<gid>"` to the service and give `/srv/jukem/data` the same owner.

There's no `TZ` variable. The zone that matters is the one stored in Settings, which jukem uses for every schedule; a container `TZ` would only change log timestamps and invite people to change one and not the other.

`group_add` takes the host's numeric GID. Writing `audio` resolves the name inside the Alpine image, where audio is GID 18, while Debian, Ubuntu, and Raspberry Pi OS hosts give `/dev/snd/*` GID 29. The container would land in the wrong group and ALSA would report permission denied. On an Alpine host both numbers are 18.

`/dev/snd` is bind-mounted with a cgroup rule rather than passed with `devices:`. The `devices:` form copies the nodes that exist when the container starts, so a USB DAC plugged in later never appears. With the bind mount, new nodes show up, and the rule for character major 116 (ALSA) permits access to them.

Docker masks `/proc/asound` inside containers, so device discovery goes through `aplay -l` and `/dev/snd`, which is what bare metal uses too.

If the host runs PipeWire or PulseAudio, it may hold the sound card open; a headless appliance host normally runs neither. Docker Desktop, on every OS including Linux, runs containers inside a VM with no access to the host's sound card, so the Docker target means Docker Engine on a Linux host.

### Adding jukem to another Alpine-based image

```dockerfile
FROM alpine:3.24
ARG VERSION=1.0.0
RUN wget -qO /etc/apk/keys/jukem.rsa.pub \
      https://github.com/OWNER/jukem/releases/download/v$VERSION/jukem.rsa.pub \
 && wget -qO /tmp/jukem.apk \
      https://github.com/OWNER/jukem/releases/download/v$VERSION/jukem-$VERSION-$(apk --print-arch).apk \
 && apk add --no-cache /tmp/jukem.apk && rm /tmp/jukem.apk
USER jukem
ENTRYPOINT ["jukem", "serve", "--config", "/etc/jukem/config.yaml"]
```

### Building from source

With Go 1.27 or later:

```sh
git clone https://github.com/OWNER/jukem.git
cd jukem
scripts/package.sh arm64 0.1.0      # writes dist/jukem-0.1.0-aarch64.apk, unsigned
```

Install it with `apk add --allow-untrusted ./jukem-0.1.0-aarch64.apk`; the flag is needed only because local builds aren't signed. For an image: `docker buildx build --platform linux/arm64 -t jukem .`

### Removing

`rc-service jukem stop && rc-update del jukem && apk del jukem`. The database, logs, and music stay on disk.

## Dockerfile

```dockerfile
# syntax=docker/dockerfile:1

# Build stage: runs natively on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETARCH
ARG VERSION=0.0.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN scripts/package.sh "$TARGETARCH" "$VERSION"

# Runtime stage: the same apk that bare-metal installs use.
FROM alpine:3.24
COPY --from=build /src/dist/ /tmp/dist/
# The package was built in the stage above and never left the build, so no signature is needed.
# Afterwards the service user is re-created as 1000:1000 so bind mounts owned by the host's first user just work.
RUN apk add --no-cache --allow-untrusted /tmp/dist/jukem-*.apk \
 && rm -rf /tmp/dist \
 && deluser jukem && (delgroup jukem 2>/dev/null || true) \
 && addgroup -g 1000 jukem \
 && adduser -D -H -u 1000 -G jukem -s /sbin/nologin jukem \
 && addgroup jukem audio \
 && chown -R jukem:jukem /var/lib/jukem /var/log/jukem /srv/jukem
ENV JUKEM_RUNTIME=docker \
    JUKEM_LISTEN=":8080" \
    JUKEM_LOG_FILE=""
USER jukem
EXPOSE 8080
VOLUME /var/lib/jukem
HEALTHCHECK --interval=30s --timeout=5s CMD ["jukem", "healthcheck"]
ENTRYPOINT ["jukem", "serve", "--config", "/etc/jukem/config.yaml"]
```

`.dockerignore`:

```text
.git
build/
dist/
*.rsa
```

Go compiles natively once per platform, and only the short `apk add` runs emulated. The package creates the service user with a system UID, which is right on bare metal, where UID 1000 is usually a person; the image re-creates it as 1000 so the compose file's `chown` line is the common case. The container listens on 8080 because an unprivileged process in a container can't bind 80; compose publishes it as 80. `JUKEM_LOG_FILE=""` sends logs to stdout for `docker logs`, and `JUKEM_RUNTIME=docker` makes jukem phrase its fix-it messages for a container. `jukem healthcheck` calls the local health endpoint, so the image needs no curl or wget.

## Data, migrations, and upgrades

All state is in one SQLite file, `/var/lib/jukem/jukem.db`, opened with:

```text
PRAGMA journal_mode = WAL
PRAGMA synchronous  = NORMAL
PRAGMA foreign_keys = ON
PRAGMA busy_timeout = 5000
```

One write connection and a small read pool avoid `SQLITE_BUSY` under WAL. Nothing about this appliance justifies a database server.

The pure-Go driver is slower than the cgo one, roughly by half on heavy batch work, and it doesn't matter here: the nightly library update is MPD rebuilding its own database, not jukem writing SQLite, and the heaviest thing jukem ever writes is a reference rewrite when a folder is moved. Those run in transactions of a few hundred rows so a large move never holds the write connection for long.

### The upgrade invariant

> Every migration either completes or is rolled back. The database is always at a version some released binary understands, never in the middle of a migration.

A failure at migration 3 of 5 leaves the database at version N+2: consistent, and the next start resumes from there. That is weaker than "all or nothing" for the whole upgrade, and it's the guarantee SQLite can give cheaply.

Migrations are numbered SQL files embedded in the binary, and a `schema_version` table records the highest one applied. On startup:

1. If the database's version is higher than the binary knows, jukem enters maintenance mode (below) and says which versions are involved. An old binary must never write to a newer schema, which is exactly what happens during a rollback if nothing checks.
2. If the version is lower, jukem copies the database with `VACUUM INTO` to `/var/lib/jukem/snapshots/jukem-v<version>-<timestamp>.db`, then applies each migration in its own transaction. A failure rolls that migration back and enters maintenance mode with the snapshot path in the message. The last five snapshots are kept.

Rolling back is: stop the service, install the older package (`apk del jukem` first if apk refuses the downgrade), copy the matching snapshot over `jukem.db`, remove `jukem.db-wal` and `jukem.db-shm`, start the service.

### Maintenance mode

Refusing to start is the wrong reaction to a fatal problem when a supervisor is watching: `respawn_max=0` and a two-second delay turn "refuse" into thirty restarts a minute and a log full of the same line. So a schema the binary doesn't know, a failed migration, an unreadable data directory, or a config file that doesn't parse all put jukem into maintenance mode instead. It doesn't touch the database or start MPD; it serves only the Health page, which shows the reason and the fix, on the normal port, and answers `/healthz` with `503 Service Unavailable`. The `jukem healthcheck` subcommand exits non-zero on anything other than 200, so a container in maintenance mode is marked unhealthy. That's a mark, not a restart: Docker doesn't restart unhealthy containers by itself, so there's no loop, and anyone who runs an auto-heal tool gets a restart that lands back in the same clear message. The process stays up, the supervisor stays quiet, and the person sees what happened at the same address they always use.

### What to back up

`/var/lib/jukem` holds everything jukem owns: the database, snapshots, playlists, and TLS material. Copying that directory while the service is stopped is a complete backup; the music root is separate and usually much larger. There's no in-app export or import. A backup format is a second schema to migrate, and `cp -a` already works.

Nothing grows without bound on an SD card. Play history keeps 90 days or 50,000 rows, whichever is smaller; dismissed alerts are kept 30 days; both are trimmed by the nightly job, and the limits are settings.

## MPD supervision

The requirement is narrow: MPD must not outlive jukem, because an orphaned MPD keeps the sound card open and the next MPD fails with "device busy". `mpdctl` is a small component with one invariant and no cleverness in the hot path.

| Situation | Behavior |
|---|---|
| jukem starts | Recover first (below), render `mpd.conf`, remove any stale socket, start `mpd --no-daemon`, wait for the socket |
| MPD exits on its own | Restart with backoff (1, 2, 4, 8, capped at 30 seconds); after five failures in five minutes, raise an alert and keep trying |
| jukem gets SIGTERM or SIGINT | Stop MPD, wait up to five seconds, SIGKILL if needed, then exit |
| jukem is SIGKILLed or panics | MPD is orphaned. The supervisor restarts jukem within seconds, and startup recovery kills the orphan before starting a new MPD. |

Startup recovery is the mechanism that makes the invariant hold, so it's deliberate rather than heuristic. jukem writes MPD's PID, its `/proc/<pid>/stat` start time, and the config path to `<data_dir>/mpd/mpd.pid` when it starts MPD. On the next start it reads that file and kills the process only if all of these still match: the PID exists, the start time is identical (so a recycled PID is never killed), `/proc/<pid>/exe` resolves to the same MPD binary, and the process is owned by jukem's own UID. As a backstop for a missing PID file, it scans `/proc` for processes owned by its UID whose `cmdline` names its generated config path. SIGTERM, then SIGKILL after five seconds.

Earlier drafts used Linux's `Pdeathsig` to have the kernel kill MPD automatically. It works, but it fires when the OS *thread* that started the child exits, not the process, so it needs a goroutine pinned with `runtime.LockOSThread()` for MPD's entire lifetime. That's an invisible invariant someone would eventually break, and the failure mode is music stopping for no apparent reason. Startup recovery covers the same case with ordinary code.

The failure modes worth testing directly, on real hardware: SIGTERM jukem, SIGKILL jukem, panic jukem, SIGKILL MPD, MPD hanging, MPD exiting immediately (bad config), the USB DAC disappearing, and the USB DAC coming back.

## Audio output devices

### Identity

Naming a card by index (`hw:1,0`) is unsafe, because indexes depend on the order drivers load. Naming it by ALSA card ID is better but still not unique: two identical USB DACs become `Device` and `Device_1` in enumeration order. For an appliance, playing through the wrong physical output after a reboot is a real failure, so each device gets a stable identity.

For each card, jukem reads `/sys/class/sound/card<N>/device` and records, where available, the USB serial number, the USB vendor and product IDs, the sysfs device path (which encodes the physical port), and the ALSA card ID. Matching a saved selection to a present card goes in that order:

1. Vendor ID, product ID, and serial number together, if the device reports a serial. Serials aren't trusted alone: cheap DACs often ship every unit with the same one, so if two present cards tie on this rule, it's skipped.
2. Vendor and product ID plus sysfs path, which pins it to a physical port
3. ALSA card ID
4. Nothing matches: playback stops, the UI marks the device missing, and the watchdog raises an alert

Non-USB devices (HDMI, DAC HATs, onboard codecs) have stable sysfs paths, so rule 2 covers them. Where `/sys` isn't readable, jukem falls back to the card ID and says so on the Audio screen.

### Discovery

`LC_ALL=C aplay -l` lists playback devices:

```text
card 1: Device [USB Audio Device], device 0: USB Audio [USB Audio]
```

From each line jukem takes the card ID, description, and device number, and builds the ALSA name `plughw:CARD=Device,DEV=0`. `plughw` lets ALSA convert sample rate, format, and channel count, which cheap USB DACs and HDMI outputs often need.

Detection is event-driven: an inotify watch on `/dev/snd` sees device nodes appear and disappear, which works identically on bare metal and through the Docker bind mount, unlike netlink uevents which don't cross a network namespace. A card's nodes appear one at a time over a few hundred milliseconds, so the watcher waits one second after the last event before rescanning, rather than reading a half-registered card. A rescan also runs every 60 seconds as a safety net, and on demand from the Audio screen. Inotify on a bind-mounted `/dev/snd` can occasionally miss a node the host kernel creates, so the safety net matters more in Docker; while the selected output is missing, the interval drops to 15 seconds, which puts the worst case for a replugged DAC at a quarter of a minute instead of a full one. Polling `aplay -l` every 10 seconds was pointless work on a device that changes twice a year.

Loopback and dummy cards are hidden unless "show all" is turned on.

### Selecting the output

jukem writes one `audio_output` block per present device and, after every MPD start, enables the selected one and disables the rest by name (MPD numbers outputs by config position, and positions shift when the config is regenerated). Switching between devices already in the config is instant; a newly plugged device means regenerating the config and restarting MPD, after which the state file restores the queue and the reconciler resumes.

There's no automatic fallback to a second device. A fallback output is rarely the one connected to the amplifier, so switching to it trades silence for confusion. If the selected device disappears, the watchdog raises an alert, and the reconciler keeps retrying, so music resumes by itself when the device comes back.

### Hardware mixer

There's no test tone. Playing any track through the normal path tells you more than a generated sine wave, and it needs no special handling: an external `speaker-test` would have meant taking the device away from MPD and then explaining to the intent tracker why MPD stopped.

A muted ALSA control is a common cause of silence, but having jukem hunt for something called Master, PCM, Speaker, or Headphone and set it to 80% at every startup is the kind of helpfulness that surprises people, especially on HDMI and multi-channel cards. So it's opt-in per device, and it's a setup step rather than something managed continuously:

- Audio > *device* > Hardware level shows the playback controls the card exposes, with the current level and mute state for each.
- Choosing a control and a level applies it once; play something to confirm it.
- A "reapply at startup" checkbox, off by default, records that exact control and level and reapplies it when MPD restarts. jukem only ever touches a control someone chose.

Day-to-day volume is MPD's software mixer, which behaves the same on every device.

## MPD configuration

Template rendered by jukem:

```text
music_directory       "/srv/jukem/music"
playlist_directory    "/var/lib/jukem/playlists"
db_file               "/var/lib/jukem/mpd/database"
state_file            "/var/lib/jukem/mpd/state"
sticker_file          "/var/lib/jukem/mpd/sticker.sql"
bind_to_address       "/var/lib/jukem/mpd/mpd.sock"
zeroconf_enabled      "no"
restore_paused        "yes"
auto_update           "no"
max_playlist_length   "20000"

audio_output {
    type        "alsa"
    name        "usb-Device-0"
    device      "plughw:CARD=Device,DEV=0"
    mixer_type  "software"
}
```

The socket lives in the data directory, the same path in both environments, readable only by jukem, which removes a stale one before starting MPD. With `bind_to_address` set to a socket path, MPD opens no TCP port, and `zeroconf_enabled "no"` stops it advertising itself.

`restore_paused "yes"` handles an awkward case: if power drops at 21:00 mid-track and returns at 07:00, MPD would otherwise resume playing hours before it should. It comes back paused and the scheduler decides.

`auto_update "no"` is deliberate. MPD's inotify watching misses changes on NFS and SMB mounts and can exhaust `fs.inotify.max_user_watches` on large libraries, which makes library freshness unpredictable. jukem owns updates instead, which makes them explainable:

| Trigger | Scope |
|---|---|
| Uploads finish, or a file operation completes | The affected folder |
| Nightly, at a configurable hour | Full |
| Rescan button, or `POST /library/rescan` | Full |

`max_playlist_length "20000"` sets the supported ceiling: MPD's default of 16384 is easy to exceed with a large folder, and a queue beyond 20,000 tracks makes the queue view and MPD's own memory use unpleasant. A source that resolves to more than that loads the first 20,000 tracks in order and the UI says so.

jukem holds one connection in `idle` mode for change events and a small pool for commands. MPD closes command connections idle past `connection_timeout` (60 seconds), so the pool pings or reconnects before use.

## Scheduler

### Sources

A source resolves to an ordered list of tracks. Every kind resolves through the same contract, which is what makes the scheduler testable:

```text
resolve(source) -> ordered []track, truncated bool
```

| Source | Resolution |
|---|---|
| Directory | Every audio file below that path, in case-insensitive path order |
| Playlist | The playlist's entries, in playlist order |

Resolution is a snapshot taken when a window starts, not a live view: files added mid-window appear the next time the window starts or when someone reloads it. That makes "what is playing" answerable at any moment. Shuffle then works on the resolved list, so the order on screen always matches what MPD holds.

Internet radio streams aren't a v1 source. They bring buffering, metadata oddities, network failures, and their own watchdog semantics, and the library needs to be rock solid first.

### Schedule model

```text
weekly rules  ->  date exceptions  ->  source  ->  play options
```

| Field | Notes |
|---|---|
| `name`, `enabled` | |
| `days` | Any combination of Monday to Sunday. A day means the day the window *starts*: Friday 22:00 to 02:00 is a Friday rule. |
| `start_time`, `end_time` | Local wall-clock times. An end earlier than the start runs past midnight. |
| `source_type`, `source_ref` | `directory` (a path under the music root) or `playlist` (a playlist ID) |
| `shuffle` | Play the resolved list in random order |
| `volume` | Applied when the window starts, clamped to the configured limits |

Scheduled music always repeats until its window ends; nobody wants a window that plays a folder once and then goes quiet, or one track on a loop for eight hours. Crossfade, fade-in, and fade-out are global settings rather than per-window ones, which removes three fields and a class of "why is this window different" questions.

Date exceptions are a separate table keyed by date: silent all day, different hours, or a different source for that day. One calendar covers holidays, closures, and one-off events. An exception governs its whole calendar day: any interval from a neighboring day that reaches into it is clipped at the day boundary, so a Friday 22:00 to 02:00 window stops at midnight when Saturday is marked closed, and a Saturday exception with its own hours starts at 00:00 if it says so. Seasonal date ranges on rules and resuming a source where it left off are both worth having later, and neither is worth the model complexity now.

### Intervals, not days and times

Reasoning about "days plus times plus overnight" at every call site is where scheduling bugs come from. Consider a rule for Friday 22:00 to 02:00 and another for Saturday 00:00 to 03:00: they belong to different weekdays, different rows, and they clearly overlap.

So rules are expanded into concrete intervals exactly once, in one place. Given the store's time zone, jukem expands every enabled rule and exception into absolute start and end instants across a rolling window from yesterday to eight days out, applies exceptions over rules, and caches the result until something changes. Everything downstream works on those intervals:

- The reconciler asks which interval contains "now".
- Conflict detection compares intervals pairwise, so the Friday and Saturday rules above collide and the editor says which rule conflicts.
- The UI's week view draws the same intervals the scheduler uses, so what you see is what will happen.

### Daylight saving

Expansion is where DST is handled, with explicit rules rather than whatever the time library happens to do:

| Case | Policy |
|---|---|
| The local time doesn't exist (spring forward, 02:30 where 02:00 jumps to 03:00) | Use the next valid instant, 03:00. A window that should have started still starts. |
| The local time happens twice (fall back, 01:30) | Use the first occurrence. A window starts on time, and an end time that repeats doesn't extend the window by an hour. |
| A window spans the transition | It runs an hour shorter or longer in real time. Wall-clock boundaries are what people mean. |

Both transitions, in both hemispheres, are unit-tested against a fixed clock.

### Who owns playback

Rather than deriving behavior from several booleans, jukem tracks one owner at a time:

| State | Meaning | Ends when |
|---|---|---|
| `SCHEDULED` | The schedule decides | An override is created, or the scheduler is switched off |
| `OVERRIDDEN` | A person's action decides, with an end time | The override expires or is cleared |
| `MANUAL` | The scheduler is switched off in Settings; the person decides entirely | The scheduler is switched back on |
| `UNAVAILABLE` | jukem can't act: the clock is untrusted, MPD is down, or the selected output is missing | The cause clears |

Precedence when several apply: MPD not running is always `UNAVAILABLE`, since nothing can play. An untrusted clock or a missing output device makes the owner `UNAVAILABLE` only while the scheduler is on; with the scheduler off the owner stays `MANUAL` and the problem shows as a warning, because a person pressing play in manual mode has no schedule to get wrong.

The status badge names the owner in plain words: "Morning Mix until 11:00", "Paused from web UI, schedule resumes 14:00", "Scheduler off", "Clock not set". Every API response that describes the player includes the same owner and reason.

### Reconciliation loop

The loop runs every 5 seconds and immediately after any schedule, override, scheduler-switch, or MPD event.

```text
actual = mpd.status()

if owner == MANUAL:
    do nothing
elif owner == UNAVAILABLE:
    hold; surface the reason in the UI and to the watchdog
elif owner == OVERRIDDEN:
    if now >= override.ends_at or override.finished:
        clear the override; owner becomes SCHEDULED; run again
    else:
        enforce the override's own intent (playing or stopped)
else:
    want = interval_containing(now)
    if want is none:
        if actual.state == play: fade out, stop
    elif want.program_key != loaded.program_key:
        if want.source == loaded.source and want.options == loaded.options:
            keep playing; apply volume; adopt the new program key
        else:
            fade out, load want, apply options and volume, play, fade in
    elif actual.state != play:
        play; alert after repeated failures
```

The loop enforces the owner, the program, and the play state. Volume and play options are applied once at a window's start, so someone who turns the volume down isn't overruled five seconds later.

### Program identity

The queue in MPD has no memory of where its tracks came from, so jukem records what it loaded. Storing just the source is too weak: two rules can point at the same folder with different volume or shuffle, and a date exception can replace one with another mid-day.

The loaded program is therefore identified by a **program key**: the rule or exception ID plus the start instant of the occurrence, alongside the resolved source, options, and a queue generation counter that increments every time jukem loads a queue. A new key means a new program; the source-and-options comparison above means back-to-back windows that really are identical don't restart the music.

### Knowing why MPD stopped

"MPD is stopped" can mean the queue finished, or that MPD failed, the device vanished, a command was rejected, or someone cleared the queue. Inferring "the tracks finished" from a stopped state alone would end a Play Now override at the wrong moment.

So every command jukem sends is recorded as an intent: what was asked for, against which queue generation, and at what time. A stop is classified as *finished* only when the last intent was play, no stop was requested since, the queue generation is unchanged, MPD reports no error, and the last played track was the final one in the queue. Anything else is *failure*: the reconciler retries, and the watchdog alerts if it keeps happening. Only *finished* ends a Play Now override.

### Overrides

| Override | Ends when |
|---|---|
| Until next scheduled event | The schedule next changes: a window starts or ends, including changes from a date exception |
| Timed | After 15 minutes, an hour, or a custom duration, or at the next scheduled event if that comes first |
| Play Now | The chosen tracks finish, or at the next scheduled event if that comes first |

No override outlasts the next scheduled event, so the appliance can't be left silent by someone who pauses the music and goes home. Pause and stop create an "until next scheduled event" override unless a timed option is picked; Resume schedule clears any override. If the schedule has no event in the coming week, an override lasts until Resume schedule.

Overrides are stored with their source (the web UI, or an API key's name) and their *mode*, not a fixed end instant. "Until next scheduled event" is resolved against the current intervals every time the reconciler runs, so editing the rules while an override is active changes when it ends, as it should. A timed override stores its duration's end instant and still ends early at the next event. A pause survives a reboot until it expires.

### Switching the scheduler off

Settings > Schedule has an on/off switch that puts the appliance in `MANUAL`: the reconciler does nothing, any active override is cleared, Resume schedule disappears, transport and queue actions work without creating overrides, and the watchdog raises no dead-air alerts. It survives reboots, and since MPD restores its state paused, a rebooted appliance in manual mode stays silent until someone presses play. Switching it back on applies the current schedule immediately.

### Clock

Wrong time means the wrong music at the wrong hour, so jukem doesn't guess. The clock has a source, and scheduling only runs when there is one:

| Source | How |
|---|---|
| `ntp` | The kernel reports the clock synchronized through `adjtimex`, which also works inside a container |
| `rtc` | `/sys/class/rtc/rtc0` exists and the system time is later than the binary's build timestamp |
| `manual` | Someone signed in entered the date, time, and time zone, during this boot |
| `none` | None of the above: the owner is `UNAVAILABLE`, the UI shows "Clock not set", and nothing is scheduled |

The `rtc` source exists because the kernel's synchronized flag is only ever cleared by an NTP daemon. A Pi with a battery-backed RTC and no network boots with the right time and the kernel still reports it unsynchronized, so trusting `adjtimex` alone would leave an offline appliance silent forever. The build-timestamp check is the sanity floor: a dead RTC battery reports a date years in the past, and that's caught rather than scheduled against.

A Raspberry Pi without an RTC boots at an arbitrary time, so an appliance with no internet access wants either a local NTP server or a battery-backed RTC (a DS3231 module, or the Pi 5's built-in RTC with a battery). Inside Docker the host's clock is what counts, and `adjtimex` reads the host kernel's status.

The manual path is explicit: a form with date, time, and zone, not a "yes, the time is fine" button. jukem runs unprivileged and can't set the system clock, so it stores the difference between what was entered and the system clock and applies that offset to all scheduling, marking the source `manual` with the time it was set. It also shows the console command that fixes the system clock properly. As soon as the kernel reports synchronization, the offset is discarded and the source becomes `ntp`.

The offset is only meaningful for the boot it was entered in: without an RTC the system clock is arbitrary again after a restart, and applying last week's offset to it would schedule against nonsense. jukem stores the kernel's `boot_id` with the offset and discards the offset when the boot ID changes. The consequence, stated plainly in the UI, is that an appliance with no RTC and no NTP needs the time entered again after every reboot. That's the price of never playing the wrong schedule, and an RTC module costs a few dollars.

### Fades

MPD crossfades between tracks but has no fade-to-stop. jukem steps `setvol` down over the fade period (20 steps across 4 seconds works), stops, then restores the previous volume so the next start isn't silent. A fade-in sets volume to 0 before `play` and steps back up.

A four-second fade can't run inside a loop that ticks every five seconds, and running it in a goroutine alone isn't enough either, because the next tick would see a falling volume and a playing state and try to correct them. So a fade is a state the reconciler knows about. It runs in its own goroutine with a cancellable context, and while one is in progress the loop does exactly one thing: check that the fade's reason still holds, and cancel it if not. A Play Now that arrives during a fade-out cancels the fade and takes the player; a window that ends during a fade-in cancels it and starts the fade-out. When a fade finishes, the loop runs normally on its next tick.

Both fades rely on the software mixer, and both bypass the configured volume floor: the floor applies to requests from people and API clients, not to jukem's own fades, or a floor of 20 would turn every fade-out into a hard cut.

## Library and file management

### Browsing

Browsing and search go through MPD's database (`lsinfo` for folders, `search` for text), which returns tags and durations without touching the disk. Folders come back in pages of 200, so a folder with thousands of tracks stays responsive on a Pi.

### Queue actions

A single file, a multi-file selection, a folder, and a playlist all offer the same three actions, in row menus and in select mode:

| Action | Queue | Schedule |
|---|---|---|
| Play Now | Replaces the queue with the chosen tracks and starts playing | Creates an override that ends when the tracks finish or at the next scheduled event, whichever comes first |
| Play Next | Inserts the tracks directly after the current track | No override; the current program continues after them |
| Add to Queue | Appends the tracks to the end of the queue | No override |

Tracks go in a predictable order: a selection in the order shown on screen, a folder in path order including subfolders, a playlist in playlist order. Play Next inserts at MPD's relative positions (`+0`, `+1`, ...) so a selection doesn't come out reversed, and inserts at the top when nothing is playing. Neither Play Next nor Add to Queue starts playback on its own.

With shuffle on, queue position no longer decides what plays next, so Play Next also gives the inserted tracks MPD's highest priority (`prioid 255`); they play before the rest of the queue, shuffled among themselves, and jukem resets each to 0 once played. Add to Queue just adds to the shuffled pool. The confirmation toast mentions shuffle when it's on.

Anything added to the queue survives until the schedule starts a different program, which replaces the queue.

### Bulk upload: what people see

In Library, Upload opens a panel targeting the current folder: a Bootstrap offcanvas from the right on desktop, from the bottom on phones. Files can be added with a multi-file picker, a folder picker on desktop browsers, or by dragging files and folders onto the list, and dragged folders keep their structure.

Before anything is sent, the panel shows the file count, total size, how many will be skipped as unsupported, how many already exist, and whether the disk has room. A conflict setting chooses skip (the default) or replace.

During the transfer, an overall bar shows bytes sent, files done, speed, and time remaining. Active and failed files each get a row with their own bar and status; finished files collapse into a count, so a 500-file batch stays readable. Pause lets in-flight files finish and holds the rest, Cancel aborts, Retry failed re-queues errors. Closing the panel doesn't stop anything: a badge on the Upload button keeps showing progress while you browse elsewhere.

When everything is done, the panel shows "Scanning library" until MPD's update finishes, then a toast: "42 tracks added to Christmas".

### Bulk upload: browser side

The upload manager lives in the app shell rather than a view, so changing screens doesn't interrupt it, and the page warns before a close mid-batch. It sends three files at a time, one request per file, using XMLHttpRequest because `fetch` still lacks reliable upload progress across browsers.

Network errors and 5xx responses retry up to three times with backoff. 4xx responses (unsupported type, too large, no space) fail immediately with the server's message on that row. A sleeping phone suspends its uploads. The Screen Wake Lock API would prevent that, but browsers only expose it in a secure context, and the default here is plain HTTP, so it works only when HTTPS is switched on. Over HTTP the panel says to keep the screen on during a large batch. The same secure-context rule applies to `navigator.clipboard`, so the copy buttons fall back to `document.execCommand('copy')`, which still works everywhere that matters.

A failed file restarts from the beginning. Music files are 5 to 50 MB and this is a LAN, so resumable uploads (tus, or anything like it) would be machinery built for a problem nobody has hit yet. The one-request-per-file design leaves room to add it if that ever changes.

### Bulk upload: server side

The browser first sends the planned relative paths to `POST /api/v1/library/files/check`, which returns those that already exist, so skipped files are never sent.

Each file then goes to `PUT /api/v1/library/files?path=<relative path>&conflict=<policy>` as a raw body. The handler rejects the request unless the path stays inside the music root, the extension is allowed, and there's room:

- **Path safety** is a boundary: resolve the path and require that it stays under the music root, and reject `..` segments, absolute paths, control characters, empty segments, and segments over 255 bytes. Names beginning with a dot are rejected too, for a practical reason rather than a security one: MPD's updater skips hidden names, so a file uploaded into `.foo/` would never appear in the library and nobody would know why. That rule also keeps MPD out of jukem's `.jukem-tmp` directory without any ignore file.
- **Size** is enforced with `http.MaxBytesReader` regardless. When `Content-Length` is present, a preflight check rejects oversized or space-hungry uploads before a byte is written; when it's absent (a legitimate chunked upload) the stream is cut off at the limit and the partial file is discarded.
- **Space** must exceed the file size plus a configured reserve.

The body streams into `<music root>/.jukem-tmp/<random>.part`, gets fsynced, has the conflict policy applied, and is renamed into place. The temp directory is inside the music root, so a root that is itself an NFS or SMB mount is still one filesystem and the rename is atomic: a file is either complete in the library or absent, and MPD never sees a partial upload.

The exception is a nested mount, a subfolder of the root that's a separate filesystem, where the rename fails with `EXDEV`. Copy-and-delete would be the obvious fallback and the wrong one, since a half-copied file in the library is precisely what MPD would index. Instead, on `EXDEV` the upload is written again as a hidden `.part` file in the destination folder and renamed within that filesystem, which keeps the guarantee. Dot-names are invisible to MPD, so the partial file is never indexed in either location.

jukem counts uploads in progress. Once none has been active for five seconds it runs a single MPD update on the deepest folder covering everything uploaded since the last rescan. Hundreds of files, including folder uploads with subfolders, produce one rescan. Leftover `.part` files older than an hour are cleaned at startup and hourly.

### Other file operations

Rows offer rename, move, and delete alongside the queue actions; select mode applies Play Now, Play Next, Add to Queue, add to playlist, move, and delete to many items at once. New folder is available everywhere.

Delete is permanent and always confirms first, showing how many files and folders will go and warning if a playlist or schedule refers to them.

When jukem moves or renames something itself, it updates the references it owns in the same operation: playlist entries pointing at moved files are rewritten, schedules whose source directory moved are repointed, and do-not-play entries follow the file. Doing a normal thing in the UI shouldn't quietly break a playlist. Changes made outside jukem are still caught by a nightly check that flags playlist entries whose files have vanished and shows them in the UI.

If the music root is read-only (a NAS export, a `:ro` Docker volume), jukem detects it and hides upload and file operations.

Upload settings live in Settings > Library: maximum file size (default 500 MB), allowed extensions (default `mp3 flac ogg opus m4a aac wav aiff`), and free-space reserve (default 1 GB).

### Folder permissions

Music copied in as root is owned by root, and every write operation in those folders fails. Checking it is worth doing, but crawling every folder under the music root at each startup means thousands of file creations on a large or NAS-backed library, which is slow exactly where it hurts.

So permissions are checked where they matter:

- Before an upload, new folder, move, or delete, jukem checks only the folders that operation touches. A failure returns an error naming the folder and how to fix it, rather than a bare "permission denied".
- Settings > Maintenance > Check library permissions crawls the whole tree on demand and lists every folder that isn't writable.

The fix depends on where jukem runs, so the message does too. On bare metal it's `chown -R jukem:jukem <folder>`. Under Docker (`JUKEM_RUNTIME=docker`) the message says to run it **on the Docker host**, not in the container, since the container has no Docker access and can't do it, and suggests either matching the container's `user:` to the directory's owner or changing ownership on the host. The command is offered with a copy button and a note that a recursive `chown` is the wrong move on a NAS mount with its own UID mapping.

### Playlists

Playlists are `.m3u` files in MPD's playlist directory, so other tools can read them and they survive a database rebuild. Each has a SQLite row with a stable ID, and schedules point at the ID, so renaming a playlist doesn't break a schedule.

### Changing the music root

Changing it regenerates `mpd.conf`, restarts MPD, and triggers a full rescan. Playlist entries are relative to the root, so the UI warns that they only resolve if the new root has the same structure.

## Web UI

### Libraries

| Library | Why |
|---|---|
| Bootstrap 5.3.8, CSS and JS bundle | Responsive grid, navbar, offcanvas, modals, toasts, forms, and dark mode through `data-bs-theme`. The bundle includes Popper. |
| Bootstrap Icons | Transport and navigation icons that match Bootstrap. Vendored as published; no trimming step, because shaving a few hundred kilobytes off a LAN appliance's first load isn't worth a build step that can break. |
| SortableJS | Drag-to-reorder for the queue and playlists |

Everything else is browser APIs: `fetch`, `EventSource`, XMLHttpRequest for upload progress, and Wake Lock during uploads when the page is served over HTTPS. No jQuery, framework, bundler, or CDN. Vendored files are committed under `web/vendor/` with versions in `VERSIONS`, so the UI works with no internet access.

The UI calls the same JSON API a remote app will use, so there's one surface to secure and test.

### Structure

`index.html` is a shell with a hash router (`#/library/Christmas/2026`); views are ES modules rendering into `<main>`. Navigation never reloads the page, which keeps the event stream and in-flight uploads alive. Assets are embedded in the binary and served gzipped with a long cache and a version query string; `index.html` is never cached. Bootstrap's system font stack means no web fonts.

### Live updates

Events say *what changed*, never *what the state is now*. The client responds by refetching the resource it cares about over REST:

```text
SSE event: {"type":"player"}     -> client refetches GET /status
SSE event: {"type":"library"}    -> client refetches the current folder
```

There's no event history and no `Last-Event-ID` replay, so a phone that reconnects after six hours costs nothing: it refetches status and the view it's on and is immediately correct. The server keeps a small per-client buffer and drops a client that can't keep up, which is harmless because reconnecting resynchronizes. REST is the only source of truth.

### Layout

At Bootstrap's `lg` breakpoint and up, a top navbar holds the five sections (Now Playing, Library, Playlists, Schedule, Settings) and a now-playing bar is fixed to the bottom. On phones the sections move to a fixed bottom tab bar with icons, and the now-playing bar sits above it and expands on tap.

The owner badge is always visible: green for scheduled, amber for overridden, grey for manual or stopped, red for unavailable. Transport buttons are `btn-lg`, rows are at least 44px tall, and dark mode follows the device.

Reordering works by dragging, with Move up, Move down, and Move to... in every row menu. Drag is the nice way; it shouldn't be the only way, particularly on a phone where dragging fights with scrolling.

### Screens

| Screen | Contents |
|---|---|
| Now Playing | Track and source, transport, volume, override buttons (15 min, 1 hour, until next scheduled event) and Resume schedule, paged queue with remove and reorder, do-not-play button |
| Library | Breadcrumb, search, folders and tracks, row menu (Play Now, Play Next, Add to Queue, add to playlist, rename, move, delete), select mode, New folder, Upload, permission warnings |
| Playlists | List with the three queue actions per playlist, detail view with reorder, add tracks from a library picker, missing-file warnings |
| Schedule | Week view of the expanded intervals, rule list with enable switches, rule editor in a modal, exceptions calendar |
| Settings | Seven sections, below |
| Health | Plain-language system status. Not a sixth tab: tapping the owner badge opens it, and it's linked from Settings > System. Requires login. |

Settings is one page with seven sections, not a second navigation tree:

| Section | Contents |
|---|---|
| Playback | Volume floor and ceiling, crossfade, fade in and out, default shuffle |
| Audio | Output device, hardware level |
| Schedule | Scheduler on/off, time zone, clock status and manual set |
| Library | Music root, upload limits, allowed extensions, free-space reserve, nightly rescan time |
| Security | Password, API keys, HTTPS |
| System | Version, alerts and webhook, log file, play history |
| Maintenance | Rescan library, check permissions, database snapshots, restart service |

### Health

`/healthz` is for Docker. The Health screen is for people, and it's the first thing to ask someone to open:

```text
System healthy

Audio      OK, USB Audio Device
MPD        OK, running 4d 2h
Scheduler  OK, Morning Mix until 11:00
Clock      Synchronized (NTP)
Library    OK, 8,412 tracks, last scan 03:00
Storage    72% used, 41 GB free
Last track change   2m ago
```

Anything not OK says what's wrong and what to do. The same data is available at `GET /api/v1/health`.

### First run

The wizard covers password, time zone, music root, output device, and a first schedule. The music root step is a server-side directory browser, since on bare metal the library is usually a NAS mount or a USB drive somewhere under `/mnt` or `/media`; in Docker it's pre-filled with the bind mount. Then the wizard does one more thing: it offers to play a track from the library, and the last step doesn't complete until the person confirms they heard it.

Setup is finished when music has come out of the speakers, not when a configuration row exists.

### Untrusted text

Track titles and file names come from uploaded files, so the UI treats them as hostile. Views build the DOM through a helper that sets `textContent` and attributes only, never `innerHTML`. The server sends `Content-Security-Policy: default-src 'self'; img-src 'self' data:; media-src 'self'; connect-src 'self'; frame-ancestors 'none'`, so no inline scripts or style attributes are possible. `data:` covers the small SVGs in Bootstrap's CSS.

## REST API

Under `/api/v1`, with its OpenAPI 3.1 spec at `/api/v1/openapi.json` so a remote app can generate a client. Errors use RFC 9457 `application/problem+json`. Player, queue, override, and device endpoints accept an optional `zone` parameter defaulting to `main`, which is a cheap way to keep a future multi-zone version from breaking clients. Internally there is one player, one queue, and one scheduler; no zone abstraction exists in the code until zones do.

| Method | Path | Purpose |
|---|---|---|
| GET | `/status` | Player state, current track, owner and reason, output device, health summary |
| GET | `/events` | SSE change notifications |
| POST | `/player/{action}` | `play`, `pause`, `stop`, `next`, `previous`. Play, pause, and stop create an override. |
| PUT | `/player/volume` | 0 to 100, clamped to the configured limits |
| PUT | `/player/options` | Shuffle |
| POST | `/player/seek` | Position in seconds |
| GET | `/queue` | Paged |
| POST | `/queue` | Queue action on files, folders, or playlists: `play_now`, `play_next`, `add` |
| DELETE | `/queue/{id}` | Remove an entry |
| POST | `/queue/move` | Reorder |
| GET | `/library/browse?path=&page=` | Folders and tracks, paged |
| GET | `/library/search?q=` | Tag and filename search |
| GET | `/library/storage` | Space, read-only status, known permission problems |
| GET | `/library/preview?path=` | Audio stream for browser preview, with Range support. Cookie sessions only: `<audio>` can't send a bearer header, so a remote app needs the signed-URL variant listed under Later. |
| POST | `/library/files/check` | Which of the given relative paths already exist |
| PUT | `/library/files?path=&conflict=` | Upload one file as the raw body |
| POST | `/library/folders` | Create a folder |
| POST | `/library/move` | Move or rename, updating playlist and schedule references |
| POST | `/library/delete` | Permanent delete |
| POST | `/library/rescan` | Full rescan |
| POST | `/library/permissions/check` | Crawl the tree and report unwritable folders |
| GET, POST, PUT, DELETE | `/playlists`, `/playlists/{id}` | Playlist CRUD |
| GET, POST, PUT, DELETE | `/schedules`, `/schedules/{id}` | Rule CRUD with interval conflict checks |
| GET, POST, PUT, DELETE | `/schedule-exceptions`, `/schedule-exceptions/{date}` | Date exceptions |
| GET | `/schedules/intervals?from=&to=` | Expanded intervals for a date range; what the week view draws |
| GET, POST, DELETE | `/override` | Read, create, or clear |
| GET, PUT | `/scheduler` | Read or set the scheduler on/off switch |
| GET, PUT | `/clock` | Clock source and status; set date, time, and zone manually |
| GET | `/devices` | Detected outputs with identity and presence |
| POST | `/devices/rescan` | Rescan |
| PUT | `/devices/default` | Select the output |
| PUT | `/devices/{id}/mixer` | Set and optionally remember a hardware control level |
| GET | `/history` | Play history |
| GET, DELETE | `/alerts`, `/alerts/{id}` | Active alerts, dismiss one |
| GET | `/health` | Full health detail |
| GET | `/system/info` | Version, schema version, uptime, runtime |
| GET | `/system/directories?path=` | Server-side directory listing, for choosing the music root |
| GET, PUT | `/settings` | Every setting the UI exposes: volume limits, crossfade and fades, time zone, music root, upload limits, nightly rescan hour, alert webhook, HTTPS, retention |
| POST | `/auth/login`, `/auth/logout` | Start or end a web session |
| PUT | `/auth/password` | Change the password and sign out every session (web session only) |
| GET, POST, DELETE | `/api-keys`, `/api-keys/{id}` | List, create, revoke API keys (web session only) |

`/healthz`, unauthenticated and minimal, sits outside `/api/v1` for container health checks.

## Security

### Login

One login: a password set in the setup wizard, no user accounts or roles, and signing in gives full control. An appliance owned by one household or one shop doesn't need RBAC, invitations, or groups.

The web UI gets a session cookie (`HttpOnly`, `SameSite=Lax`, and `Secure` when TLS is on) plus a CSRF token header on state-changing requests. Lax rather than Strict: Strict drops the cookie when someone follows a link to the jukebox from elsewhere, so a signed-in person lands on the login page for no reason, and Lax already blocks cross-site POSTs. The CSRF header covers what's left. Several devices can be signed in at once; changing the password signs them all out. `jukem reset-password` on the console recovers a forgotten one. The password is hashed with argon2id and login attempts are rate-limited per IP.

### API keys

Programs authenticate with API keys, separate from the login.

| Property | Detail |
|---|---|
| Creation | Settings > Security, each with a name such as "Counter tablet app" |
| Format | 32 random bytes, shown once, sent as `Authorization: Bearer <key>` |
| Storage | SHA-256 hash, plus name, creation time, last-used time and IP, and an optional expiry |
| Access | Everything except login, password changes, and key management, which need a web session so a leaked key can't mint more keys or lock the owner out |
| Revocation | Delete the key; the next request using it is rejected |

Opaque random keys checked against stored hashes revoke instantly, which JWTs can't do without extra machinery. Every key currently carries full access; scopes (read, playback, library, schedule) are a natural addition once there's a second kind of client, and the storage already has a place for them.

### Transport

HTTP on the LAN by default. An appliance that greets its owner with a browser certificate warning has taught them to click through warnings, which is worse than the plaintext it was protecting against, and a self-signed certificate also blocks a future mobile app.

HTTPS is one switch in Settings > Security, for anyone who wants it: upload a certificate and key (from a home CA, Let's Encrypt via DNS, or Tailscale), and jukem serves TLS and redirects HTTP. It will also generate a self-signed certificate on request, with the warning about what that means in a browser. For access away from home, Tailscale or WireGuard is the recommendation rather than a forwarded port, and the README says so plainly.

CORS is off by default, with an allowlist setting. The service runs as the unprivileged `jukem` user with access only to the audio devices, the music root, and its own directories. Upload hardening is described under Library and file management, and the UI's defenses against hostile tags under Untrusted text.

## Alerts and watchdog

The watchdog notices when the schedule says play and reality disagrees: MPD stopped or erroring, elapsed time not advancing (a hung USB DAC does this), the output device missing, MPD failing to restart, or the disk nearly full. It retries, restarts MPD, and then raises an alert.

Alerts appear in the UI first: a banner on every screen and an entry on the Health screen, which is enough for a device on a home network. A webhook is the one optional delivery method: a JSON POST to a URL, with a preset that formats the body for ntfy. Email means SMTP servers, credentials, TLS, and deliverability problems, none of which belong in v1.

## Recommended features

### In v1

| Feature | Why |
|---|---|
| Watchdog and dead-air alerts | Silence when music should be playing is the failure that matters |
| Health screen | One page that answers "is it working?" without reading logs |
| Volume floor and ceiling | Nobody, including an API client, can set it to 100% at 07:00 |
| Date exceptions | Holidays, closures, and one-off events without editing weekly rules |
| Browser preview | Audition a track on headphones before putting it on the speakers. The only path where audio reaches a browser. |
| Do-not-play list | One tap on Now Playing keeps a track out of all future scheduled playback |
| Play history | What played and when, which also answers "what was that track?" |
| First-run wizard ending in music | Setup is done when sound comes out |

### Later

| Feature | Notes |
|---|---|
| Resume position | Ordered sources continue where they stopped instead of replaying the same opening tracks each morning |
| Seasonal rules | Date ranges on weekly rules, once date exceptions prove insufficient |
| Internet radio | A stream source, and possibly a fallback when the library is unavailable |
| Announcements | A clip every N tracks or at set times; queue insertion is easy, ducking music under a voice needs a second ALSA path and is not |
| API key scopes | Read-only keys for status displays, once there's a second kind of client |
| Signed preview URLs | Short-lived URLs so a remote app can stream previews without a cookie |
| ReplayGain | Deliberately out for now. It rewrites tags in the music files, and a normalization pass is a lot of machinery for a library that's usually consistent already. |
| Multiple zones | Separate DACs with one MPD per zone. The `zone` API parameter exists; the internals stay single-player until then. |
| Artist separation | MPD's shuffle can repeat an artist. Writing a better shuffle is a rabbit hole worth entering only if it actually annoys someone. |
| mDNS name | `jukem.local` via Avahi; needs host networking in Docker |
| Kiosk display | Read-only now-playing page for a spare screen |
| apk repository | Signed repository so `apk upgrade` picks up new versions, once releases have settled |
| MQTT or Home Assistant | State and commands where a home already runs automation |

## Hardware and operations

Use a USB DAC or a DAC HAT. The Raspberry Pi's 3.5mm jack has an audible noise floor through a real amplifier.

SD cards corrupt when power drops mid-write. On a Pi, consider Alpine's data-disk mode, which runs the OS from RAM and keeps `/var` on persistent storage, ideally a USB SSD; jukem's database and logs live under `/var`, so they persist. Keep the music library on separate storage from the OS, and cap the log file.

Playing recorded music in a business, as opposed to a home, generally needs a public performance licence (PRS and PPL in the UK; ASCAP, BMI, SESAC, and GMR in the US) unless the music is licensed for commercial background use. The play history helps if reporting is ever required.

## Build order

| Step | Scope | Done when |
|---|---|---|
| 1 | `jukem serve` with a health endpoint and a placeholder page, nFPM packaging, `tools/apksign`, OpenRC files, Dockerfile, CI with install tests | `apk add ./jukem-*.apk` without `--allow-untrusted` then `rc-update add jukem default` works on a Pi and an x86 box, port 80 answers, the image runs on both platforms, and an upgrade restarts the service |
| 2 | SQLite store, migrations, schema version guard, snapshots, maintenance mode | An old binary against a newer schema serves the Health page with the reason instead of crash-looping, and a migration failure leaves a working database and a snapshot |
| 3 | MPD supervisor, config template, device identity and discovery | Sound comes out on Alpine and in Docker; SIGKILLing jukem leaves no MPD running; unplugging and replugging a USB DAC recovers; two identical DACs stay distinguishable across a reboot |
| 4 | Login, sessions, API keys | Every endpoint after this is protected from the start, and a revoked key fails on its next request |
| 5 | UI shell, SSE notifications, player API, Now Playing | A desktop and a phone agree within a second; a client reconnecting after hours is immediately correct |
| 6 | Library browse, search, queue actions | A 10,000-track library browses without lag on a Pi 4, and Play Next on a 50-track selection plays them in order after the current track |
| 7 | Bulk upload, on-demand permission checks | 300 files from a phone show accurate progress, survive a Wi-Fi drop, and trigger one rescan; a root-owned folder produces the right fix message on both bare metal and Docker |
| 8 | Playlists, reference-aware moves | Moving a file in the UI leaves every playlist and schedule pointing at it |
| 9 | Scheduler: interval expansion, resolver, reconciler, overrides, exceptions, clock sources | Tests pass for midnight-crossing rules, both DST transitions, overlap detection across weekdays, exceptions clipping overnight windows, and overrides ending at the next event; an RTC-only box schedules; a power cut mid-window resumes the right source |
| 10 | Watchdog, alerts, webhook, health screen, play history | Unplugging the DAC raises an alert and replugging resumes playback |
| 11 | First-run wizard, README | A fresh Pi reaches music through the wizard alone |
