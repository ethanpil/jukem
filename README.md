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

The `jukem` package installs a binary that manages and monitors `mpd` on
the server. JukeM monitors mpd to ensure everything is healthy. You control
the JukeM instance via its web interface.

JukeM is developed as a GoLang application, with help from LLMs.

## Installation

Do these steps as root. Replace `VERSION` with the number of the release.

```sh
VERSION=0.1.8
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

To go to a newer version, get the new package and do `apk add` again. The
service starts again by itself. You can leave out releases: the database
moves up from any older version in one step.

Settings > System tells you when a newer release exists, and gives the
commands. jukem asks GitHub once a day. It does not install anything by
itself.

## More

- [Guide](docs/guide.md): paths, Docker, a reverse proxy, backups, the
  configuration file and development
- [Changelog](CHANGELOG.md)
- [Design notes](PLAN.md)

## Licence

MIT. See [LICENSE](LICENSE).

The logo and favicon are the "Music Library 2" icon from the Solar icon set
by 480 Design (Solar Bold Duotone Icons), under the Creative Commons
Attribution 4.0 licence: https://creativecommons.org/licenses/by/4.0/.
jukem changed only the colours for the light and dark themes.

The interface uses the Onest and IBM Plex Mono fonts, both under the SIL
Open Font License 1.1. The files are in `web/vendor/fonts/` with their
licences.
