# CLAUDE.md

Notes for an agent that works on jukem. Read this before a change. It gives
the goals, the structure, the words the project uses, the traps, and the
decisions behind the code.

## 1. What jukem is

jukem is a jukebox appliance for a shop, a bar or a home. It plays music on a
weekly schedule through the sound hardware of its own machine. A web UI and a
REST API control it. MPD does the playback; jukem owns MPD.

Design rules of the product:

- **One appliance, one owner.** One password, no user accounts, no roles.
- **It must keep playing.** A fault gives an alert and a retry, not a crash.
- **It must explain itself.** Every message says what is wrong and what to do.
- **It is small.** One Go binary with the UI inside it, one SQLite file, one
  MPD child process.
- Target machines: a Raspberry Pi or a small x86 box on Alpine Linux, with a
  library of up to about 20,000 tracks.

Read `PLAN.md` for the original design and its reasons. Read `docs/guide.md`
for the operator manual and `README.md` for the short introduction.

## 2. How to work in this repository

- **Language rule.** Comments, documentation, README, changelog and commit
  messages use ASD-STE100 Simplified Technical English. Short sentences (20
  words or fewer), active voice, no idioms, no contractions, no semicolons.
  This rule comes from `C:\Users\Admin\.claude\CLAUDE.md` and it applies to
  every file here.
- **Simplicity first.** Write the minimum code that solves the problem. No
  speculative options, no abstraction for one caller. See
  `C:\Code\.claude\CLAUDE.md`.
- **Surgical changes.** Touch what the task needs. Do not reformat or
  "improve" code beside it.
- **Commits.** Group one logical change per commit. Do not put unrelated
  fixes in one commit. The remote is `origin`
  (https://github.com/ethanpil/jukem.git); push when the user asks for it.
- **Changelog.** Put a user-visible change in `CHANGELOG.md` under
  `[Unreleased]`, with the short commit hash.
- **Verification.** A change is not done until it builds, the tests pass, and
  a UI change was seen in a browser.

## 3. Environment traps (Windows host)

The repository is on Windows, and the product runs on Linux.

- **Bash heredocs fail.** A Bash command with several large heredocs (about
  180 lines or more) fails with `unexpected EOF`. A quoted heredoc also turns
  `\n` into a real newline and corrupts Go strings. Write source files with
  the Write and Edit tools. For a big mechanical edit, write a Python patch
  script to the scratchpad with Write, then run it with Bash.
- **Line endings.** Python must open a file with `newline=''`, or it writes
  CRLF and `gofmt` fails. `.gitattributes` sets `eol=lf` and
  `core.autocrlf=false`.
- **Check both platforms.** Run `go vet ./...` and `GOOS=linux go vet ./...`.
  Several files are Linux only (`*_linux.go`).
- **The UI is embedded.** `web/` goes into the binary with `go:embed`, so a
  UI change needs a server restart before a browser sees it.
- **Dev server.** `.claude/launch.json` holds the `jukem-dev` configuration
  (port 18080, data dir `C:/tmp/jk`). Use the Browser pane tools. The pane
  reports a zero-size window until `resize_window` sets one, and screenshots
  time out when the window is hidden; read the page with `javascript_tool`
  instead.
- **No MPD on Windows.** Playback, scans and devices only work on the test
  server. `go test ./...` still passes here.
- Go 1.27, `CGO_ENABLED=0`. The SQLite driver is `modernc.org/sqlite`, so
  `go test -race` is not available on this machine.

## 4. Repository map

```
cmd/jukem/        serve, healthcheck, reset-password, version; logging
internal/api/     huma v2 REST API, auth, static file handler
internal/app/     wiring: the App builds every component and holds the state
internal/audio/   ALSA device discovery, identity, hardware mixer
internal/config/  bootstrap config file and environment
internal/events/  SSE hub
internal/library/ paths, browse, uploads, playlists, file operations
internal/mpdctl/  MPD supervisor, connection pool, mpd.conf, orphan recovery
internal/player/  MPD commands, queue, intents, announcements
internal/scheduler/ intervals, reconciler, fades, clock, announcement times
internal/store/   SQLite store, migrations, snapshots
internal/update/  daily check of the newest GitHub release
internal/watchdog/ health report and alerts
web/              index.html, maintenance.html, app/ modules, vendor/
packaging/        nfpm config, OpenRC files, install scripts
scripts/package.sh  build a binary and an apk
tools/apksign/    RSA-SHA256 apk signer
docs/guide.md     operator manual
```

About 20,000 lines of Go, JavaScript and CSS, without the vendored files.

## 5. How the parts work together

- `cmd/jukem serve` loads the config, builds the `App`, and serves HTTP on
  one listener. A fatal problem at startup does not exit. jukem serves the
  maintenance page and answers `/healthz` with 503, because an exit would
  make the supervisor restart it every two seconds.
- `app.Build` opens the store, runs the migrations, scans the audio devices,
  writes `mpd.conf`, starts MPD, and starts these loops: the MPD event
  watcher, the scheduler, the watchdog, the nightly job and the update check.
- **jukem owns MPD.** It renders the configuration, starts `mpd --no-daemon
  --stderr` as a child, and restarts it with backoff. A device change or a
  music root change rewrites the configuration and restarts MPD.
- **The scheduler is a reconciler.** Every 5 seconds, and after every kick,
  `Tick` compares what should play with what MPD does, and corrects it.
- **Events, not polling.** The MPD idle connection turns into SSE events
  (`player`, `queue`, `library`, `devices`, `schedule`, `alerts`, `settings`,
  `health`, `upload`). An event says only what changed. The client then
  refetches over REST.
- **One writer.** The store has one write connection and a small read pool.
  Code inside a `Tx` must use the transaction.

## 6. The words this project uses

- **Program:** what a schedule rule plays in a window. A program key is the
  rule id plus the start of the occurrence.
- **Owner:** who decides playback now. `SCHEDULED`, `OVERRIDDEN`, `MANUAL`
  (the scheduler is off) or `UNAVAILABLE` (MPD down, no device, no clock).
- **Override, called "stop the scheduler" in the UI:** a person's decision
  that outranks the schedule. `until_next` ends at the next scheduled start
  or end. `timed` ends after its minutes, or at the next event if that comes
  first. `play_now` ends when the chosen tracks finish. The UI shows this in
  the scheduler card (`web/app/automation.js`).
- **Announcement:** one file that plays at its own time, whatever the music
  does. The music fades out, the announcement plays, the music fades in at
  the same point. The reconciler is held meanwhile. A late announcement is
  skipped after a grace of two minutes. Announcements play in groups, one
  group at a time. An announcement that comes due, or a test play, while a
  group plays joins that group. The music comes back after the last one.
- **Source:** a folder, a playlist or a stream URL. A stream needs no
  library.
- **Do not play:** a track that stays out of scheduled playback. It is also
  removed from the queue at once.
- **Intervals:** the expansion of rules and exceptions into instants for a
  date range, in the configured zone.
- **Hold (`Scheduler.Suspend`):** an internal pause of the reconciler while a
  person's action runs, so a tick cannot undo it.

## 7. Web UI conventions

- Plain ES modules, no build step, no framework. Bootstrap 5.3 gives the
  behaviour (modals, menus, toasts) and the vendored files are in
  `web/vendor/`.
- **Build the DOM with `h()`** from `web/app/dom.js`. It sets `textContent`
  and attributes only. Never use `innerHTML`: track titles and file names
  come from uploaded files.
- **CSP is `default-src 'self'`.** No inline script and no `style` attribute.
  `h()` writes a style through `el.style.cssText`. The logo SVG is the one
  exception: it carries a dark-mode style rule, so its response has its own
  policy.
- **Design system.** `web/app/app.css` holds the tokens: Onest for text, IBM
  Plex Mono for figures and paths, a teal accent, `oklch` colours with a
  fallback for an older browser. The design comes from the "JukeD Interface
  Design" Claude Design project; the product keeps the name jukem.
- **A view** is a function `(main, rest) => ({ onEvent(type), destroy() })`.
  `destroy` must clear every timer, listener and Sortable instance.
- **State** lives in `web/app/main.js`: `state.status`, `state.session`,
  `state.statusListeners`. The event chain refreshes the status before the
  view hears the event, so a view reads fresh data.
- Every list row is at least 44 px tall. The layout must work at 375 px with
  no horizontal scroll.

## 8. API conventions

- huma v2 under `/api/v1`, errors as `application/problem+json`, OpenAPI at
  `/api/v1/openapi.json`.
- A browser uses the session cookie plus the `X-CSRF-Token` header. A program
  uses `Authorization: Bearer <key>`.
- **Web session only** for the endpoints that can take the appliance over:
  `PUT /settings`, the system endpoints, the directory listing and the
  webhook test. An API key must not be able to move the music root and
  delete the database.
- `openPaths` in `internal/api/auth.go` lists what works without a
  credential: login, setup, session, health, system info and the OpenAPI
  files.
- `store.Settings` accepts an unknown field, so a page from another release
  can still save.
- Forwarding headers (`X-Forwarded-For`, `X-Forwarded-Proto`) are read only
  from an address in `trusted_proxies`. The default is the loopback address.

## 9. Database rules

- Migrations are `internal/store/migrations/000N_name.sql`, one file per
  change, no gap in the numbers, never changed after a release.
- SQLite cannot change a `CHECK`. Build the new table, copy the rows, remove
  the old table, rename the new one. `0003_stream_source.sql` is the example.
- `TestMigrateFromEachOlderVersion` upgrades from every older schema. Keep it
  passing. A person can leave out releases.
- A database from a newer release puts jukem in maintenance mode with the
  rollback steps. jukem copies the database to `snapshots/` before a
  migration and keeps the newest five, and the newest of each version.
- `LoadSettings` puts the stored values over the defaults, so a new setting
  has a value on an old database.

## 10. Packaging and release

- `scripts/package.sh <amd64|arm64> <version>` builds the binary and an apk
  with nFPM. With `APK_SIGNING_KEY` it signs the package with
  `tools/apksign` (RSA-SHA256, the form abuild produces).
- The signing key does not exist yet. The releases carry an **unsigned** apk,
  and the readme installs it with `--allow-untrusted`.
  `.github/workflows/release-apk.yml` attaches the apk to a published
  release. `release.yml` is the signed pipeline for later.
- OpenRC runs the service with `supervise-daemon`, as the `jukem` user, with
  `cap_net_bind_service` for port 80. `post-upgrade.sh` restarts the service.
- The Docker image installs the same package. It listens on 8080 and runs as
  UID 1000.
- jukem serves plain HTTP only. HTTPS belongs to a reverse proxy.
- Settings > System reports a newer release. jukem only reads the GitHub API
  once a day. It downloads nothing and installs nothing.

## 11. Testing habits

```sh
gofmt -l .
go vet ./... && GOOS=linux go vet ./...
go test ./...
node --check web/app/<file>.js      # after every JS edit
```

- The live test appliance is **10.0.0.136** (Alpine LXC, real MPD, about
  2,000 real tracks, no sound card yet). SSH with
  `ssh -i "c:/Code/claude-ssh-id/claude_id_ed25519" -o IdentitiesOnly=yes
  root@10.0.0.136`.
- **Never delete or move anything in `/srv/jukem/music`.** It is the user's
  library. Count the files before and after an upgrade.
- Upgrade it with `scripts/package.sh amd64 <version>`, `scp` to `/tmp`, then
  `apk add --allow-untrusted /tmp/jukem-<version>-x86_64.apk`. The package
  restarts the service.
- Check a UI change in the Browser pane against the dev server, and against
  the test server when it needs MPD.

## 12. Decisions already made

Do not reopen these without a reason from the user.

- **MPD does the playback.** jukem does not decode audio. It owns the MPD
  process, so bare metal and Docker behave the same.
- **No HTTPS in jukem** (2026-09-15). The certificate switch, the TLS
  listener and the redirect are gone. A reverse proxy adds HTTPS. jukem marks
  the session cookie Secure when a trusted proxy reports HTTPS.
- **Maintenance mode instead of an exit** for a fatal startup problem.
- **The schedule week starts on Sunday** in the week view and the rule
  editor. The API day bits do not change: Monday is 1, Sunday is 64.
- **The scan bar has no percentage.** MPD reports no progress, and its track
  count stays frozen until a scan ends. The bar shows movement and the time.
- **"Recently played" comes from the play history**, not from the queue.
  With shuffle on, the queue order is not the play order.
- **Shuffle is the order of the queue**, not MPD's random mode (2026-09-17).
  jukem loads a shuffled program in a random order, and MPD's random mode
  stays off. So the queue shows the play order. jukem holds the shuffle state
  (`queue_shuffle` in the state table), because MPD does not. The last track
  of a repeating queue shuffles the tracks before it, for a new pass.
- **The queue list starts at the current track** and follows it.
- **Browser preview** plays a track in the browser only. It is in the row
  button and in the row menu.
- **The allowed extension list gates uploads only.** It does not affect
  playback or the library scan.
- **The logo** is one SVG that sets its own colour for dark mode. Firefox
  ignores `media` on an icon link, so two files did not work. The icon is
  from the Solar icon set by 480 Design, CC BY 4.0, with changed colours.
- **No in-app backup format.** `cp -a` of the data directory is the backup.

## 13. Faults already corrected

Each line is a real fault that a review found. Do not bring it back.

- `listall` with a file-only parser broke every folder source. Use a search
  that returns songs.
- A percent sign in a file name corrupted an MPD command. gompd sends the
  built command through `Fprintf`, so double the percent sign (`literal()` in
  `internal/player/player.go`). A newline in a path injected a second
  command.
- The local-time search was anchored on UTC midnight, so an evening rule in a
  western zone collapsed. It now takes the candidate instants from the zone
  offsets.
- An expired override came back days later, because its end was computed
  again from a rolling cache. The loop now removes it.
- A wedged MPD held every connection, including the watchdog. The pool now
  has deadlines.
- `Shutdown` waited for the event stream, so every stop exited with status 1.
  The request contexts now end when the shutdown starts.
- The queue view used a stale track position after a queue edit, and a drag
  used the wrong base. The status now refreshes on a queue event, and the
  list renders with the offset that fetched it.
- The health report answered 503 for a read-only music root or a missing
  device. Those are warnings.
- The login limiter counted every client behind a proxy as one. It now reads
  the client address from a trusted proxy.
- argon2 ran without a bound on an open endpoint. It now has a semaphore, and
  setup checks for a password before it hashes one.
- Snapshot pruning kept the oldest file of a version, not the newest.
- An upload failed when the music root is a symlink, and the free space
  reserve was not enforced across parallel uploads.
- A settings page from an older release got 422 on every save.
- The scan bar stayed on screen when a scan ended before the first check.
- MPD started without `--stderr`, so its log went nowhere.

## 14. Known gaps

- A browser that signed in over HTTPS cannot sign in over plain HTTP on the
  same host name until the cookie expires. The guide says to use one address.
- "Recently played" rows have no queue actions.
- The hardware paths (ALSA output, USB DAC, HDMI) are still untested with a
  real sound card. The test server has no audio device.
- The signing key for the apk does not exist, so the releases are unsigned.
