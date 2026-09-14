package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Settings holds every setting the UI exposes. Bootstrap settings (listen
// address, data directory, log file) live in the config file instead.
type Settings struct {
	SetupComplete bool `json:"setup_complete" doc:"True after the setup wizard has finished"`

	// Playback
	VolumeMin      int  `json:"volume_min" minimum:"0" maximum:"100" doc:"Volume floor for people and API clients"`
	VolumeMax      int  `json:"volume_max" minimum:"0" maximum:"100" doc:"Volume ceiling for people and API clients"`
	Crossfade      int  `json:"crossfade" minimum:"0" maximum:"30" doc:"MPD crossfade in seconds"`
	FadeIn         int  `json:"fade_in" minimum:"0" maximum:"30" doc:"Fade-in at a window start in seconds"`
	FadeOut        int  `json:"fade_out" minimum:"0" maximum:"30" doc:"Fade-out at a window end in seconds"`
	DefaultShuffle bool `json:"default_shuffle" doc:"Shuffle new schedule rules by default"`

	// Audio
	ShowAllDevices bool            `json:"show_all_devices" doc:"Show loopback and dummy cards"`
	OutputDevice   *DeviceIdentity `json:"output_device,omitempty" doc:"Saved output device identity"`

	// Schedule
	SchedulerEnabled bool   `json:"scheduler_enabled" doc:"Off puts the appliance in manual mode"`
	TimeZone         string `json:"time_zone" doc:"IANA zone name used for every schedule"`

	// Library
	MusicRoot         string   `json:"music_root" doc:"Directory MPD reads music from"`
	UploadMaxBytes    int64    `json:"upload_max_bytes" minimum:"1" doc:"Largest upload accepted"`
	AllowedExtensions []string `json:"allowed_extensions" doc:"Upload file extensions without the dot"`
	FreeSpaceReserve  int64    `json:"free_space_reserve" minimum:"0" doc:"Bytes that must stay free after an upload"`
	NightlyRescanHour int      `json:"nightly_rescan_hour" minimum:"0" maximum:"23" doc:"Hour of the nightly full rescan and trim"`

	// Security
	HTTPSEnabled bool     `json:"https_enabled" doc:"Serve TLS and redirect HTTP"`
	CORSOrigins  []string `json:"cors_origins" doc:"Allowed origins for cross-site API use; empty disables CORS"`

	// System
	AlertWebhookURL    string `json:"alert_webhook_url" doc:"JSON POST target for alerts; empty disables"`
	AlertWebhookPreset string `json:"alert_webhook_preset" enum:"generic,ntfy" doc:"Body format for the webhook"`
	HistoryDays        int    `json:"history_days" minimum:"1" doc:"Play history retention in days"`
	HistoryRows        int    `json:"history_rows" minimum:"100" doc:"Play history retention in rows"`
	AlertDays          int    `json:"alert_days" minimum:"1" doc:"Dismissed alert retention in days"`
}

// DeviceIdentity is the stable identity of an output device, saved so that
// the same physical output is selected after a reboot.
type DeviceIdentity struct {
	CardID    string `json:"card_id" doc:"ALSA card ID"`
	Device    int    `json:"device" doc:"ALSA device number on the card"`
	VendorID  string `json:"vendor_id,omitempty" doc:"USB vendor ID"`
	ProductID string `json:"product_id,omitempty" doc:"USB product ID"`
	Serial    string `json:"serial,omitempty" doc:"USB serial number"`
	SysPath   string `json:"sys_path,omitempty" doc:"sysfs device path, which encodes the physical port"`
	Name      string `json:"name,omitempty" doc:"Description shown to people"`
}

// DefaultSettings returns the settings of a fresh install.
func DefaultSettings() Settings {
	return Settings{
		VolumeMin:          0,
		VolumeMax:          100,
		Crossfade:          0,
		FadeIn:             0,
		FadeOut:            4,
		SchedulerEnabled:   true,
		TimeZone:           "UTC",
		MusicRoot:          "/srv/jukem/music",
		UploadMaxBytes:     500 << 20,
		AllowedExtensions:  []string{"mp3", "flac", "ogg", "opus", "m4a", "aac", "wav", "aiff"},
		FreeSpaceReserve:   1 << 30,
		NightlyRescanHour:  3,
		AlertWebhookPreset: "generic",
		HistoryDays:        90,
		HistoryRows:        50000,
		AlertDays:          30,
		CORSOrigins:        []string{},
	}
}

const settingsKey = "settings"

// LoadSettings returns the stored settings over the defaults, so that a key
// added in a later release has a value.
func (s *Store) LoadSettings(ctx context.Context) (Settings, error) {
	set := DefaultSettings()
	if _, err := s.GetState(ctx, settingsKey, &set); err != nil {
		return set, fmt.Errorf("load settings: %w", err)
	}
	return set, nil
}

// SaveSettings validates and stores the settings.
func (s *Store) SaveSettings(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	return s.SetState(ctx, settingsKey, set)
}

// Validate checks the cross-field rules a schema cannot express.
func (set *Settings) Validate() error {
	if set.VolumeMin < 0 || set.VolumeMax > 100 || set.VolumeMin > set.VolumeMax {
		return fmt.Errorf("volume limits must satisfy 0 <= min <= max <= 100")
	}
	if set.TimeZone == "" {
		return fmt.Errorf("time zone is required")
	}
	if set.MusicRoot == "" {
		return fmt.Errorf("music root is required")
	}
	for i, ext := range set.AllowedExtensions {
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
		if ext == "" || strings.ContainsAny(ext, "./\\") {
			return fmt.Errorf("allowed extension %q is not valid", set.AllowedExtensions[i])
		}
		set.AllowedExtensions[i] = ext
	}
	if set.AlertWebhookPreset == "" {
		set.AlertWebhookPreset = "generic"
	}
	return nil
}

// ExtensionAllowed reports whether a file name has an allowed extension.
func (set *Settings) ExtensionAllowed(name string) bool {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return false
	}
	ext := strings.ToLower(name[i+1:])
	for _, a := range set.AllowedExtensions {
		if a == ext {
			return true
		}
	}
	return false
}

// String returns the settings as JSON, for logs.
func (set Settings) String() string {
	b, _ := json.Marshal(set)
	return string(b)
}
