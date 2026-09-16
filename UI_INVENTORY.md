# Jukem UI Inventory (Design Spec)

Generated from the codebase at commit `9b193f5`. Every literal label, option, and per-item
template is listed as written in code, for use as a reference while preparing visual designs.

---

## Global App Shell

Present on every authenticated page (`web/index.html` + `main.js`).

### Top navbar (`#topnav`) — desktop/tablet only (`d-lg-flex`)
- Brand link (`href="#/"`): logo (`<picture>`, dark/light SVG swap via `prefers-color-scheme`) + text "jukem"
- Nav links (`#topnav-links`), one per section:

  | Section id | Label | Icon | Route |
  |---|---|---|---|
  | `''` | Now Playing | `music-note-beamed` | `#/` |
  | `library` | Library | `folder2` | `#/library` |
  | `playlists` | Playlists | `list-ul` | `#/playlists` |
  | `schedule` | Schedule | `calendar-week` | `#/schedule` |
  | `settings` | Settings | `gear` | `#/settings` |

  Active link gets `.active`.
- `#alert-badge` — small badge, hidden until active alerts exist, shows alert count
- `#owner-badge-top` — button, opens `#/health`, text/color set dynamically (see Owner Badge below)

### Alert banner (`#alert-banner`)
Full-width warning strip, hidden unless active alerts exist. Contains: warning icon, first alert's message text, link "N alerts" / "Details" → `#/health`.

### Main content (`#main`)
Routed view renders here.

### Now-playing bar (`#now-bar`, fixed bottom, hidden until authenticated)
- `#now-bar-play` — circular play/pause button, icon toggles `bi-play-fill` / `bi-pause-fill`
- `#now-bar-text` (click → `#/`): `#now-bar-title` (bold, truncated), `#now-bar-sub` (small, secondary, truncated)
- `#owner-badge-bar` — duplicate owner badge, mobile only (`d-lg-none`)

### Tab bar (`#tab-bar`) — mobile only, fixed bottom
Same 5 sections as top navbar, icon + label. Library tab can show an upload-progress badge: `{done}/{total}`.

### Toasts (`#toasts`)
Top-right stack. Kinds used: `primary`, `success`, `danger`, `warning`, `secondary`. Auto-dismiss (default 4s). Bootstrap toast markup with close button.

### Modal root (`#modal-root`)
Empty container all modals mount into.

### Owner badge (shared component, appears in navbar + now-bar)
CSS state classes: `owner-manual` (default/secondary), `owner-scheduled` (success), `owner-overridden` (warning), `owner-unavailable` (danger). Text = `owner.reason || owner.state`, plus ` · {warning}` suffix if a warning is present. When status unreachable: text "Offline". Click → `#/health`.

---

## Shared Components (`web/app/dom.js`)

These are reused across nearly every page — worth designing once as a system.

- **Toast** — `role="status"`, colored by kind (see above), message text, close (×) button.
- **Confirm dialog** — modal with title, body (string/nodes), "Cancel" button (secondary) + confirm button (text configurable, `btn-danger` if destructive else `btn-primary`).
- **Generic modal** — title, close (×), arbitrary body, optional footer, optional `modal-lg` size.
- **Spinner** — centered Bootstrap spinner-border with visually-hidden "Loading" label.
- **Error box** — red alert showing `error.message` or a fallback string.
- **Library picker modal** — folder/file browser (breadcrumb, ".." parent nav, folder rows, optional file checkboxes, "Add all tracks here", pick button).
- **Directory picker modal** — server filesystem browser, single-select, "Use this folder" button.
- **Row-list pattern** — `.list-group-item`, min-height 44px, icon/drag-handle + truncating title/subtitle + trailing meta/action. Used by Queue, Playlist entries, History, Library entries.

---

## 1. Login — no route (shown when signed out)

Centered card, max-width 360px.

- Logo (`<picture>`, dark/light), 72×72, centered
- Heading: "jukem"
- Form:
  - Label "Password" + `<input type="password" autocomplete="current-password" required autofocus>`
  - Inline error text (hidden by default), exact strings:
    - "Wrong password." (401)
    - "Too many attempts. Wait a few minutes." (429)
    - "Sign-in failed: {message}" (other HTTP error)
    - "jukem is not reachable." (network error)
  - Submit button: **"Sign in"** (full width, large, disabled while submitting)
- Footer text: "Forgot it? On the console: jukem reset-password"

**States:** idle → submitting (button disabled) → error (message shown, password field reselected).

---

## 2. Setup Wizard — `#/setup`

6 steps: **Password, Time zone, Music, Output, Schedule, Listen**. Max-width 560px.

**Shared frame per step:** logo (36×36) + step title (h4); row of pill badges, one per step (current = primary, done = success, upcoming = secondary), numbered 1–6 with step name as tooltip; body; footer button row.

### Step 1 — Password ("Welcome to jukem")
- Copy: "Set the password for this jukebox. One login controls everything."
- Label "Password (8+ characters)" + password input (`autocomplete="new-password"`, `minlength=8`, required)
- Label "Again" + second password input (same rules, no id shown)
- Inline error: "The passwords differ." (mismatch) or raw exception message
- Footer: **"Continue"**

### Step 2 — Time zone
- Copy: "Every schedule uses this zone. Daylight saving is handled for you."
- Label "Zone" + text input with `<datalist>` of all IANA zones, defaults to browser-resolved zone
- Inline error; Footer: **"Continue"**

### Step 3 — Music
- Copy: "The folder MPD reads music from. A NAS mount or a USB drive is usually under /mnt or /media. You can also upload music later in Library."
- Label "Music root" + text input (required) + **"Browse…"** button → opens Directory Picker
- Helper text: "Music copied here as root must be given to the jukem user: `chown -R jukem:jukem <folder>`"
- Inline error; Footer: **"Continue"**

### Step 4 — Output device
- Copy: "Choose the sound card the speakers are connected to."
- Device list — each row: selection icon (`check-circle-fill` selected / `circle` unselected), device name (bold), subtext `card {index} · {id}` (mono)
- Empty state: "No playback devices found. Plug in a USB DAC or check the HAT overlay, then rescan."
- **"Rescan"** button (outline, icon `arrow-clockwise`)
- Footer: **"Continue"** + **"Skip"** (link style)

### Step 5 — First schedule (auto-skipped if a rule already exists)
- Copy: "When should music play? The whole library plays during this window. Add more rules later in Schedule."
- Label "Name" + text input (default "Opening hours", required, maxlength 100)
- Row: "Start" time input (default 09:00), "End" time input (default 17:00), both required
- Checkbox: "Also on Saturday and Sunday"
- Checkbox: "Shuffle" (default = settings' default_shuffle value)
- Inline error; Footer: **"Continue"** + **"Skip"**

### Step 6 — Listen
- Copy: "Setup is done when sound comes out of the speakers. Play the library and confirm that you hear it."
- **"Play"** button (large, primary, icon `play-fill`)
- Status text: "Starting…" → "Playing {title}" / "Playing"
- If no music (404): info alert "The library has no tracks yet, or MPD is still scanning it. Upload music in Library, then test the sound from Settings > Audio." + **"Finish without a test"** button
- Once playing — "Do you hear it?" prompt:
  - **"Yes, I hear music"** (success, icon `check-lg`) → finishes setup, toast "Setup complete", redirect to `#/`
  - **"No"** (outline) → reveals warning checklist:
    - "Is the right output device selected? Go back one step."
    - "A muted hardware control is a common cause. Settings > Audio > Hardware level."
    - "Is the amplifier on and the volume up?"
- Footer: **"Back to output"** (link, returns to step 3)

---

## 3. Now Playing — `#/` (default route)

Max-width 720px, centered.

### Track info box
- **Song present:** title (h4, truncating), subtitle "artist · album", filename (mono, small)
- **No song, MPD running:** "Nothing playing" + owner reason text
- **MPD not running:** "MPD is not running"
- **Server unreachable:** "jukem is not reachable."
- Owner badge row: reason/state text badge; extra warning badge if `owner.warning` present

### Transport row (4 buttons)
Previous (`skip-start-fill`) · Play/Pause (large circular primary, icon toggles `play-fill`/`pause-fill`) · Stop (`stop-fill`) · Next (`skip-end-fill`)

### Seek row
Elapsed time (mono) — range slider (0 to duration, step 1, disabled if no duration) — duration (mono)

### Volume row
Icon `volume-down` — range slider (0–100, step 1) — icon `volume-up` — numeric % label. Disabled/blank if volume unknown. Toast if clamped: "Volume limited to {n} by Settings > Playback"

### Options row
- **"Shuffle"** toggle button (icon `shuffle`, active/inactive style)
- If not in MANUAL state — **"Hold schedule"** button group: label "Hold schedule", buttons **"15 min"** / **"1 hour"** / **"Until next event"** (each has a descriptive tooltip)
  - If OVERRIDDEN — extra **"Resume schedule"** button (warning, icon `calendar-check`)
- If a song is loaded — **"Do not play"** button (outline-danger, icon `slash-circle`) → confirm dialog: title "Do not play", body `Keep "{title}" out of all future scheduled playback?`, confirm text "Do not play"; toast "Added to the do-not-play list" on success

### Queue heading
"Queue" (h5)

### Recently played (only shown if rows exist)
Label "Recently played" + list-group. Each row: time (HH:MM, mono), title, subtitle (artist · album, or filename). Dimmed styling (`.queue-played`).

### Queue list
- **Empty state:** "The queue is empty. Add tracks from the Library."
- **Error states:** "MPD is not running." (503) or generic error box
- **Per-row template** (draggable via handle):
  - Drag handle (`grip-vertical`)
  - Position number (mono, 1-based)
  - Title + subtitle (artist · album, or filename)
  - "next" badge (warning) if prioritized
  - Duration (mono, small)
  - Actions dropdown: "Play from here" · "Move up" · "Move down" · "Move to…" (native prompt, "Move to position (1-{total}):") · divider · **"Remove"** (danger text)
  - Currently-playing row gets `.queue-current` (left accent border)
- Shuffle note (only if shuffle on): "Shuffle is on: this is the queue order, and MPD picks the next track at random."
- Pager: **"Earlier"** (disabled at start) — "{start}–{end} of {total} tracks" — **"Show current"** (only if scrolled away) — **"Later"** (disabled at end)

---

## 4. Library — `#/library[/path]` (`?q=` for search)

### Header row
- Breadcrumb: Home icon + path segments (links), or "Search: {query}" as the trailing active crumb
- Search form: text input (placeholder "Search titles, artists, albums, file names") + submit icon button
- Tools:
  - **"Rescan"** button (icon `arrow-clockwise`, tooltip "Scan the whole music root for new, changed and removed files")
  - **"Select"** toggle button (icon `check2-square`)
  - Folder-actions dropdown (hidden during search): current-folder menu items (see Row menu below), plus — if storage writable — divider, **"New folder"**, **"Upload"**

### Scan progress banner (shown while a scan runs)
Icon `arrow-repeat` "Scanning the library" — right-aligned "{songs} tracks · {duration}" — striped/animated indeterminate progress bar. Disappears when scan completes (list reloads).

### Select bar (shown only in select mode)
"{n} selected" — **"All on this page"** / **"None"** — divider — **"Play Now"** (primary) · **"Play Next"** · **"Add to Queue"** · **"Add to playlist"** (all disabled when nothing selected) — if writable: **"Move"** · **"Delete"** (danger)

### Entry list
- **Empty states:**
  - Search with no results: "No tracks match."
  - Broken storage: "{storage.problem} Check Settings > Library."
  - Empty folder: "This folder is empty. Upload music or copy it into the music root and rescan."
- **Read-only notice** (root folder, non-search): storage problem text or "The music root is read-only. Upload and file operations are hidden."
- **Directory row:** folder icon (warning color), name (link into subfolder), actions dropdown
- **File row (normal mode):** preview button (icon `headphones`/`stop-fill`, tooltip "Browser preview: play in this browser, not on the speakers"), title/subtitle, duration (mono), actions dropdown
- **File row (select mode):** entire row is a checkbox button (`check-square-fill`/`square`), title/subtitle, duration

### Row actions menu (per entry)
Entry name header (for individual entries) → "Browser preview"/"Stop browser preview" (files only) → divider → **"Play Now"** · **"Play Next"** · **"Add to Queue"** → **"Add to playlist"** (files only) → if writable: divider → **"Rename"** · **"Move"** · **"Delete"** (danger)

### Pager (non-search)
**"Previous"** / **"Next"** (disabled at bounds) — "{page} / {pages} · {total} entries"
**Search note** (if truncated): "Only the first 500 matches are shown. Narrow the search."

### Modals/flows launched from Library
- **New folder:** native prompt "New folder name:" — error toast "A folder name cannot contain a slash." if invalid
- **Rename:** native prompt "New name:" — same slash validation, error "A name cannot contain a slash. Use Move to change the folder."
- **Move:** Library Picker modal, title "Move {n} item(s) to", **"Choose this folder"** button; toast "Already in that folder" (no-op) or "Moved {n} item(s)" (success)
- **Delete:** confirm dialog — title "Delete", body "Delete {n} file(s) and {m} folder(s) for good?"; warning alert if used by playlists ("Used by playlist(s): {names}. The entries are removed."); danger alert if used by schedules ("Used by schedule(s): {names}. Those rules will have nothing to play."); confirm text "Delete" (danger); toast "Nothing to delete: the items are already gone." or "Deleted"
- **Add to playlist modal:** "Add to playlist" title, list of existing playlists (icon, name, "{count} tracks"), plus inline create form: text input "New playlist name" (required, maxlength 100) + **"Create"** button

---

## Library Picker modal (shared: Library Move, Playlist "Add tracks", Schedule "Browse…")

- Title: contextual (e.g. "Choose a folder", "Add tracks", "Move N item(s) to")
- Breadcrumb (mono, current path)
- List: ".." parent nav (if not root); directory rows; in files mode, file rows are checkable (checkbox icon)
- Note if folder has more tracks than the picker's page limit (5): "This folder has more tracks than the picker shows. Use 'Add all tracks here' for the whole folder."
- Footer: selected-count text (files mode), **"Add all tracks here"** (files mode), and either **"Add selected"** (files mode, disabled until ≥1 selected) or **"Choose this folder"** (folder mode)
- `modal-lg` size in files mode

## Directory Picker modal (Setup wizard step 3, Settings > Library "Browse…")

- Title: "Choose the music root"
- Breadcrumb, ".." parent nav (if applicable), folder rows
- Empty state: "No subfolders."
- Footer: **"Use this folder"**

## Upload panel (Offcanvas — bottom on mobile, right on desktop)

- Header "Upload" + close
- "Into" label + mono target path (or "(music root)")
- Drop zone: icon `cloud-upload`, "Drop files or folders here", **"Choose files"** / **"Choose folder"** buttons (folder hidden on small screens)
- Conflict handling select: "Skip files that already exist" (default) / "Replace files that already exist"
- Note (non-secure context): "Keep the screen on during a large batch. A sleeping phone pauses uploads."
- Summary states:
  - Checking: "Checking files…"
  - Running: "{sentBytes} of {totalBytes} · {finished}/{total} files" + speed/ETA
  - Post-upload scanning: "Scanning library…"
  - Finished: "{done} uploaded, {skipped} skipped, {failed} failed"
  - Idle with pending: file count/size + warnings ("{n} unsupported, will not be sent"; "{n} already exist and will be skipped"; "Not enough space: {free} available")
- Progress bar (overall) + per-item rows (active/failed only): filename, status ("failed"/"retrying"/"{pct}%"), mini progress bar or error text
- Buttons: while running — **"Pause"/"Continue"** toggle, **"Cancel"**; while idle — **"Upload {n} file(s)"**, **"Retry failed"** (if failures), **"Clear"**
- Toasts: "A batch into {target} is running. Wait for it to finish." / "Upload cancelled" / "{n} track(s) added to {target}"

---

## 5. Playlists

### List — `#/playlists`
- Heading "Playlists"
- Create form: text input (placeholder "New playlist name", required, maxlength 100) + **"Create"** button
- Empty state: "No playlists yet. Create one here, or use \"Add to playlist\" in the Library."
- **Per-row template:** icon `list-ul`, name (link) + "{count} track(s)"; action button group: **"Play Now"** (primary) · **"Play Next"** · **"Add to Queue"** (icon-only, aria-labeled)

### Detail — `#/playlists/{id}`
- Breadcrumb: "Playlists" > {name}
- 404 state: "No such playlist." + back link
- Header: playlist name (h1), queue action button group (with text labels), actions dropdown: **"Add tracks"** · **"Rename"** · divider · **"Delete"** (danger)
- Missing-files warning: "{n} entries point at files that are gone. They are skipped when the playlist plays."
- Empty state: "This playlist is empty. Add tracks here or from the Library."
- **Per-row template** (draggable): drag handle, index (mono), filename / "Missing: {file}" subtitle (row highlighted if missing), actions dropdown: **"Move up"** · **"Move down"** · **"Move to…"** (native prompt) · divider · **"Remove"** (danger)
- **Rename:** native prompt "New name:"
- **Delete:** confirm dialog — title "Delete playlist", body 'Delete "{name}"? Schedule rules that use it are deleted too.', confirm "Delete" (danger)
- **Add tracks:** Library Picker (files mode) → toast "Added {n} track(s) to {name}"

---

## 6. Schedule — `#/schedule`

Max-width 1000px. Heading "Schedule" + **"New rule"** button (primary).

### Week grid
- Nav row: prev-week button, "{firstDate} – {lastDate} · {timezone}", next-week button, **"This week"** (only when offset ≠ 0)
- 7-column grid, one per day: day header, body with absolutely-positioned time blocks (tooltip: name + time range); exception intervals styled distinctly
- Empty state: "Nothing scheduled this week."

### Weekly rules list
- Conflict warning (if overlaps exist): "Rules overlap: {rule} and {other} ({at}); ... The window that started first plays."
- Empty state: "No rules yet. Add one to play music on a schedule."
- **Per-row template:** enable/disable switch, name, summary line "{dayLabel} · {start}–{end}[(next day)] · {sourceLabel}[· shuffle][· vol {n}]", **Edit** (pencil), **Delete** (danger) → confirm dialog 'Delete "{name}"?'
  - Day label logic: 127 = "Every day", 31 = "Mon–Fri", 96 = "Sat–Sun", else abbreviated day list
  - Source label: "playlist {name}" or "folder /{ref}"

### Rule editor modal
- Label "Name" + text input (required, maxlength 100)
- "Days (the day the window starts)" — button-checkbox row: Sun, Mon, Tue, Wed, Thu, Fri, Sat
- Row: "Start" / "End" time inputs (required)
- Helper text: "An end earlier than the start runs past midnight. Music repeats until the window ends."
- **Source fields** (shared block, see below)
- Validation error: "Choose at least one day."
- Footer: **"Save"**

### Source fields (shared: rule editor + exception editor)
- "Source" select: **Folder** / **Playlist**
  - Folder: text input (placeholder "Whole library") + **"Browse…"** (Library Picker, folder mode)
  - Playlist: select of playlists by name ("No playlists yet" if none)
- Checkbox "Shuffle"
- Checkbox "Set volume at the start: {n}%" + range slider (0–100, disabled unless checked)
- Validation error: "Choose a playlist."

### Date exceptions list
- Heading "Date exceptions" + **"Add date"** button
- Empty state: "No upcoming exceptions. Use them for holidays, closures and one-off events."
- **Per-row template:** date (mono), title = note or computed description, subtitle = description; description text: "Silent all day" / "{start}–{end} · {sourceLabel}" / "Normal hours · {sourceLabel}"; **Edit** / **Delete** buttons
- **Delete confirm:** title "Remove exception", body "Remove the exception on {date}?", confirm "Remove" (danger)

### Exception editor modal
- Row: "Date" input (required, disabled when editing existing) + "Note" text input (maxlength 200, placeholder "Closed for the holiday")
- "What happens" select: **Silent all day** (default) / **Different hours** / **Different source, normal hours**
- Helper text: "An exception governs its whole day. A window from the day before stops at midnight."
- If "Different hours": Start/End time row (defaults 09:00/17:00)
- If not "Silent": source fields block
- Footer: **"Save"**

---

## 7. Settings — `#/settings[/section]`

Heading "Settings" + 7 cards (deep-linkable):

### Playback
- Number inputs: "Volume floor" (0–100), "Volume ceiling" (0–100), "Crossfade (s)" (0–30), "Fade in (s)" (0–30), "Fade out (s)" (0–30)
- Checkbox "Shuffle new schedule rules by default"
- Helper text: "The floor and ceiling apply to people and API clients. Fades bypass the floor."
- **"Save playback"** button → toast "Saved"

### Audio
- Warning if `/sys` unreadable: "/sys is not readable, so devices are matched by card ID only. Two identical DACs may swap after a reboot."
- Danger alert if saved device missing: 'The selected output "{name}" is not present. Music resumes when it comes back.'
- **Per-device row:** name (+ "selected" / "virtual" badges), subtext "card {index} · {id} · device {device}[· serial {serial}][· usb {vendor}:{product}]", **"Hardware level"** button (opens mixer modal), **"Use"** button (if not selected) → toast "Output: {name}"
- Empty state: "No playback devices found. Plug in a USB DAC or check the HAT overlay."
- **"Rescan"** button
- Checkbox "Show loopback and dummy cards"

**Mixer dialog** (title "Hardware level: {device name}")
- Note: "A muted ALSA control is a common cause of silence. jukem only touches a control you choose."
- "Control" select — options: "{name} — {percent}%{ (muted)}" per hardware control
- "Level {n}%" + range slider (0–100)
- Checkbox "Reapply at every MPD start"
- **"Apply"** button → toast "Applied. Play something to confirm."
- If no controls: "This card exposes no playback volume controls." (no footer button)

### Schedule
- Switch: "Scheduler on. Off puts the appliance in manual mode: no schedule, no overrides, no dead-air alerts." → toast "Scheduler on" / "Scheduler off: manual mode"
- "Time zone" text input (datalist) + **"Save"** — helper text "Browser zone: {tz}"
- Clock status: icon + "Clock: {label}" + mono local time
  - Labels: "Synchronized (NTP)" / "Hardware clock (RTC)" / "Set by hand {time}" / "Clock not set"
  - If source is none/manual: date input + time input + **"Set clock"** button; helper text about RTC/NTP and the manual `date -s` / `hwclock -w` commands → toast "Clock set for this boot"

### Library
- "Music root" text input + **"Browse…"**
- Row: "Max upload (MB)" (min 1), "Free space reserve (MB)" (min 0), "Nightly rescan hour" (0–23), "Allowed extensions" text input
- **"Save library"** button — if music root changed, confirm dialog first: "Change the music root?" / "MPD restarts and rescans. Playlist entries are relative to the root, so they only resolve if the new root has the same structure." / confirm "Change"
- **Do not play subsection:** heading "Do not play"; empty state "No track is excluded from scheduled playback."; rows: title/filename + **"Allow again"** button

### Security
- **Password change form:** "Current" password input, "New (8+ characters)" password input, **"Change"** button; helper text "Every signed-in device is signed out." → toast "Password changed. Sign in again." then signs out
- **API keys:** heading "API keys"; rows: key name, subtext "created {date}[· last used {date} from {ip} | · never used][· expires {date}]", **"Revoke"** button → confirm 'Revoke "{name}"? The next request with it is rejected.'
- Empty state: "No API keys."
- Create form: text input (placeholder "Counter tablet app", required) + **"Create key"**
- On create: modal "New API key" — "Copy it now. It is not shown again.", readonly key value field + copy button → toast "Copied"; text "Send it as: Authorization: Bearer <key>"

### System
- Text: "Version {version} · schema {schema_version} · runtime {runtime}"
- Links: "Health page" (#/health) · "Play history" (#/history) · "API docs" (external, new tab)
- **Alerts form:** "Webhook URL (JSON POST)" url input (placeholder "https://ntfy.sh/my-jukebox"), "Format" select (Generic JSON / ntfy), **"Send test"** button → toast "Test alert sent"
- **Retention:** "History days" (min 1), "History rows" (min 100), "Dismissed alerts days" (min 1)
- **"Save system"** button
- Footer text: log file path note + logo attribution ("Logo: music library icon from the Solar Bold Duotone Icons collection, CC Attribution License.")

### Maintenance
- Buttons: **"Rescan library"** → toast "Rescan started"; **"Check library permissions"** → modal "Library permissions" (either "{n} folders are not writable." + problem list + fix command, or "Every folder is writable."); **"Database snapshot"** → toast "Snapshot written: {path}"; **"Restart service"** (danger) → confirm "Restart jukem?" / "Playback stops for a few seconds and resumes on its own." / confirm "Restart" → toast "Restarting"
- **"Sign out"** button

---

## 8. Health — `#/health`

Max-width 720px.

- Status header: icon + heading — "System healthy" (success) / "System needs attention" (warning) / "System has a problem" (danger)
- If unhealthy: danger alert with reason text + optional preformatted fix block
- **Per-alert card:** warning alert, message, optional fix text, "Since {time}[· seen {n} times]", **"Dismiss"** button
- **Checks list:** per-row — status icon, name, summary text, optional indented fix block
- Footer: "jukem {version} · schema {schema} · {runtime} · up {h}h {m}m"
- Footer: "Checked {time} · " link "Play history"

## 9. Play History — `#/history`

Max-width 900px.

- Heading "Play history"
- **Per-row template:** timestamp (mono), title/subtitle (artist · album, or filename), optional source badge
- Empty state: "Nothing played yet."
- **"Older"** button (loads 100 more; auto-hides once fewer than 100 rows return)

---

## Standalone Maintenance page (`web/maintenance.html`, served outside the SPA)

- Heading: logo (40×40, dark/light) + "jukem needs attention"
- Reason paragraph: "Loading…" → `health.reason` or "jukem is in maintenance mode." → on fetch failure: "Could not read the health report: {error}"
- Heading "What to do" + preformatted fix text block
- Footer note: "This page is served while jukem is in maintenance mode. MPD is not started and the database is not touched."
- Light/dark via `prefers-color-scheme`, centered column, max-width 42rem, no interactivity beyond the one fetch

---

## Key CSS / Layout Tokens (`app.css`)

- `--now-bar-height: 64px`, `--tab-bar-height: 56px`
- `.now-bar` — fixed bottom, `.now-bar-toggle` 48×48 circular button
- `.tab-bar` — fixed bottom, equal-width tabs, 1.35rem icons, active = primary color
- `.owner-badge` — 4 state modifiers (scheduled=success, overridden=warning, manual=secondary, unavailable=danger), max-width 40vw with ellipsis
- `.row-list .list-group-item` — min-height 44px (touch target), flex row, gap 0.5rem; `.row-title` ellipsis truncation; `.drag-handle` grab cursor
- `.transport .btn` — min-width 56px; `.btn-play` — 72×72 circular, 2rem icon
- `.queue-current` — 4px left accent border; `.queue-played` — 0.65 opacity
- `.mono` — monospace, 0.85em
- `#upload-panel.offcanvas-bottom` — height 80vh
- `.week-grid` — 7-column CSS grid, 360px tall; `.week-block` — absolutely positioned interval chip (success-subtle); `.week-exception` — warning-subtle variant
- Dropdown menus — z-index 1040 (above fixed bars)
