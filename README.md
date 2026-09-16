# jukem

jukem is a jukebox for a shop, a bar or an office. It plays music on a
weekly schedule through the sound hardware of its machine. A web page and a
REST API control it. MPD plays the audio. jukem starts MPD and keeps it
running.

Announcements play between the music: a message, an offer or an
advertisement. The music fades out for them and fades in again.

## System requirements

- Alpine Linux 3.21 or later, on x86_64 or aarch64. Make the community
  repository available. The [guide](docs/guide.md) shows how to use Docker
  instead.
- A sound card, a USB DAC, or an HDMI output.
- 256 MB of memory, and 300 MB of disk for the program. The music needs
  more.
- A correct clock, because the schedule uses it. chrony keeps the clock
  correct.
- A web browser from 2023 or later, on a computer or on a telephone.

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
service starts again by itself.

## More

- [Guide](docs/guide.md): paths, Docker, a reverse proxy, backups, the
  configuration file and development
- [Changelog](CHANGELOG.md)
- [Design notes](PLAN.md)

## Licence

MIT. See [LICENSE](LICENSE). The logo is the "Music Library 2" icon from the
Solar icon set by 480 Design, under the Creative Commons Attribution 4.0
licence. The interface uses the Onest and IBM Plex Mono fonts, under the SIL
Open Font License 1.1.
