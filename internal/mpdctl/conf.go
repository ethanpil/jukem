// Package mpdctl owns the MPD child process: it renders mpd.conf, starts
// MPD, restarts it when it exits, and supplies client connections.
package mpdctl

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Output is one audio_output block.
type Output struct {
	Name   string
	Device string
}

// Config is what the mpd.conf template needs.
type Config struct {
	MusicDir    string
	PlaylistDir string
	DBFile      string
	StateFile   string
	StickerFile string
	Socket      string
	Outputs     []Output
}

// Paths returns the fixed locations inside the data directory.
type Paths struct {
	Dir         string // <data>/mpd
	Socket      string // <data>/mpd/mpd.sock
	ConfFile    string // <data>/mpd/mpd.conf
	PIDFile     string // <data>/mpd/mpd.pid
	PlaylistDir string // <data>/playlists
}

// PathsFor returns the MPD paths for a data directory. MPD reads POSIX
// paths, so the separators are forward slashes on every platform.
func PathsFor(dataDir string) Paths {
	dir := path.Join(filepath.ToSlash(dataDir), "mpd")
	return Paths{
		Dir:         dir,
		Socket:      path.Join(dir, "mpd.sock"),
		ConfFile:    path.Join(dir, "mpd.conf"),
		PIDFile:     path.Join(dir, "mpd.pid"),
		PlaylistDir: path.Join(filepath.ToSlash(dataDir), "playlists"),
	}
}

// NewConfig builds the config for a data directory, a music root and the
// present outputs.
func NewConfig(dataDir, musicDir string, outputs []Output) Config {
	p := PathsFor(dataDir)
	return Config{
		MusicDir:    musicDir,
		PlaylistDir: p.PlaylistDir,
		DBFile:      path.Join(p.Dir, "database"),
		StateFile:   path.Join(p.Dir, "state"),
		StickerFile: path.Join(p.Dir, "sticker.sql"),
		Socket:      p.Socket,
		Outputs:     outputs,
	}
}

// Render writes the mpd.conf text. With no output present MPD gets a null
// output, so it starts and the reconciler can retry when a device appears.
func (c Config) Render() string {
	var b strings.Builder
	line := func(k, v string) {
		fmt.Fprintf(&b, "%-22s %s\n", k, quote(v))
	}
	line("music_directory", c.MusicDir)
	line("playlist_directory", c.PlaylistDir)
	line("db_file", c.DBFile)
	line("state_file", c.StateFile)
	line("sticker_file", c.StickerFile)
	line("bind_to_address", c.Socket)
	line("zeroconf_enabled", "no")
	line("restore_paused", "yes")
	line("auto_update", "no")
	line("max_playlist_length", "20000")
	line("save_absolute_paths_in_playlists", "no")
	if len(c.Outputs) == 0 {
		b.WriteString("\naudio_output {\n")
		fmt.Fprintf(&b, "    %-11s %s\n", "type", quote("null"))
		fmt.Fprintf(&b, "    %-11s %s\n", "name", quote("none"))
		fmt.Fprintf(&b, "    %-11s %s\n", "mixer_type", quote("software"))
		b.WriteString("}\n")
	}
	for _, o := range c.Outputs {
		b.WriteString("\naudio_output {\n")
		fmt.Fprintf(&b, "    %-11s %s\n", "type", quote("alsa"))
		fmt.Fprintf(&b, "    %-11s %s\n", "name", quote(o.Name))
		fmt.Fprintf(&b, "    %-11s %s\n", "device", quote(o.Device))
		fmt.Fprintf(&b, "    %-11s %s\n", "mixer_type", quote("software"))
		b.WriteString("}\n")
	}
	return b.String()
}

// quote wraps a value in double quotes the way mpd.conf expects.
func quote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}
