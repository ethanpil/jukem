# Changelog

All important changes to jukem are in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the numbers follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- A new folder did not show in the Library until a rescan. jukem now
  scans the folder after it makes it (93be018).
- The upload panel showed "null" before the upload started
  (d9c5b5c).

## [0.1.9] - 2026-09-16

### Added

- Settings > System tells you when a newer release exists. jukem asks
  GitHub once a day, shows the version and the install commands, and has a
  Check now button. It downloads nothing and installs nothing. The switch
  Check every day stops the daily request.
- A changelog, a short readme with a guide beside it, and a workflow that
  attaches an unsigned apk to each release (3d16f3d, f177274).
- The guide gives the rules for a schema change, and a test upgrades from
  each older schema to the newest one, because a person can leave out
  releases.

### Fixed

- A path test expected the Windows result on every system, so the tests
  failed on Linux.

## [0.1.8] - 2026-09-16

### Changed

- The choices in the announcement editor have clearer names: "At Specific
  Time", "At An Interval", "Random File from Specified Folder" and "Cycle
  Through Specified Folder".

## [0.1.7] - 2026-09-16

### Added

- The music fades out for an announcement and fades in again at the point it
  stopped. The fade times come from Settings > Playback.

### Fixed

- An announcement plays at its time also when no music plays. It stopped
  before when the player was quiet.
- The volume is set before the music starts again, so the first moment is
  never at the level of the announcement.

## [0.1.6] - 2026-09-16

### Added

- Announcements: one file that plays instead of the music at its own times.
  An announcement has its own days, and it plays at a time of the day or
  again and again between two times. It plays one file, any file of a
  folder, or the files of a folder one after the other. Each one can have
  its own volume.
- A schedule rule and a date exception can play a stream. The address must
  start with `http://` or `https://`.
- The Schedule page lists the announcements, with a switch for each one, an
  editor, and a button that plays one at once to hear it.

### Fixed

- The database refused a stream source, so no rule with a stream could be
  stored. A migration makes the check wider.
- Only one announcement plays at a time, and the reconciler waits for a
  reconcile that already runs, so nothing takes the queue away in the middle
  of one.
- An announcement does not go into the play history.
- An announcement follows its file or its folder after a move, and the
  delete dialog names the announcements it would break.

## [0.1.5] - 2026-09-15

### Fixed

- A row menu is above the rows below it. The buttons of those rows came
  through the open menu.

## [0.1.4] - 2026-09-15

### Added

- The Scheduler card on Now Playing, and the same card as Scheduler Status
  on Schedule. It names the rule that plays and the hours of its window, and
  it holds the controls: stop the scheduler for 15 minutes, for 1 hour,
  until the next event, or for good, and start it again.
- A new interface design: one set of colours, spacing and parts for every
  screen, with a dark form of every colour. Onest and IBM Plex Mono are
  vendored with the other libraries.

### Changed

- The hold buttons beside the player are gone. The Scheduler card has those
  controls now.
- The logo takes the colour of the interface.

### Fixed

- A row menu keeps clear of the bars at the foot of the window and turns
  upwards when it does not fit.
- The search box of the Library keeps the focus while a scan or an upload
  changes the list.
- The Settings page draws its sections again when another client changes a
  value, so a save cannot write an old value back over it.
- A browser without `oklch` colours, such as Safari 15.3, gets the same
  colours in sRGB.
- The upload panel shows the progress of each file again, and the percentage
  of the batch fits in its bar.
- Play history offers its Older button again after a failed request.

## [0.1.3] - 2026-09-15

The first appliance that works from end to end.

### Added

- Weekly schedule rules with date exceptions, a week view, and fades at the
  window boundaries.
- Library browsing, search, upload of files and folders, and file operations
  that keep playlists and rules pointing at the moved files.
- Playlists, a queue with drag to reorder, and the three queue actions.
- A player with volume limits, a do-not-play list, and holds that keep a
  person's action against the schedule.
- A first-run wizard that ends when a person confirms that sound comes out.
- Health checks, alerts with a webhook, and a play history.
- Login with one password, sessions, and API keys for other clients.
- A REST API with an OpenAPI document, and an event stream for the UI.
- An Alpine apk for x86_64 and aarch64, and a Docker image.

[Unreleased]: https://github.com/ethanpil/jukem/compare/v0.1.9...HEAD
[0.1.9]: https://github.com/ethanpil/jukem/compare/v0.1.8...v0.1.9
[0.1.8]: https://github.com/ethanpil/jukem/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/ethanpil/jukem/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/ethanpil/jukem/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/ethanpil/jukem/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/ethanpil/jukem/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/ethanpil/jukem/releases/tag/v0.1.3
