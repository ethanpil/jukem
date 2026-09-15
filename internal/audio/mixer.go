package audio

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// MixerControl is one playback control the card exposes.
type MixerControl struct {
	Name    string `json:"name" doc:"Control name as amixer shows it"`
	Percent int    `json:"percent" minimum:"0" maximum:"100" doc:"Current level of the first channel"`
	Muted   bool   `json:"muted" doc:"True when the switch is off"`
}

var (
	controlLine = regexp.MustCompile(`^Simple mixer control '(.*)',(\d+)$`)
	levelLine   = regexp.MustCompile(`Playback \d+ \[(\d+)%\](?: \[[^\]]*dB\])? \[(on|off)\]`)
	levelNoSw   = regexp.MustCompile(`Playback \d+ \[(\d+)%\]`)
)

// ParseMixer reads the output of amixer -c N scontents and returns the
// controls that have a playback volume.
func ParseMixer(out string) []MixerControl {
	controls := []MixerControl{}
	var cur *MixerControl
	hasVolume, hasLevel := false, false
	flush := func() {
		if cur != nil && hasVolume {
			controls = append(controls, *cur)
		}
		cur = nil
		hasVolume, hasLevel = false, false
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := controlLine.FindStringSubmatch(line); m != nil {
			flush()
			// amixer addresses a control with a non-zero index as 'Name',N.
			name := m[1]
			if m[2] != "0" {
				name += "," + m[2]
			}
			cur = &MixerControl{Name: name}
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Capabilities:") && (strings.Contains(t, "pvolume") || strings.Contains(t, " volume")) {
			hasVolume = true
		}
		// The first channel line sets the level; the parser skips later
		// channels.
		if hasLevel {
			continue
		}
		if m := levelLine.FindStringSubmatch(t); m != nil {
			cur.Percent, _ = strconv.Atoi(m[1])
			cur.Muted = m[2] == "off"
			hasLevel = true
		} else if m := levelNoSw.FindStringSubmatch(t); m != nil {
			cur.Percent, _ = strconv.Atoi(m[1])
			hasLevel = true
		}
	}
	flush()
	return controls
}

// Mixer runs amixer for one card.
type Mixer struct {
	run func(ctx context.Context, args ...string) (string, error)
}

// NewMixer returns a mixer that runs the amixer binary.
func NewMixer() *Mixer {
	return &Mixer{run: func(ctx context.Context, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "amixer", args...)
		cmd.Env = append(cmd.Environ(), "LC_ALL=C")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("amixer %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}}
}

// Controls lists the playback controls of a card.
func (m *Mixer) Controls(ctx context.Context, cardIndex int) ([]MixerControl, error) {
	out, err := m.run(ctx, "-c", strconv.Itoa(cardIndex), "scontents")
	if err != nil {
		return nil, err
	}
	return ParseMixer(out), nil
}

// Set applies a level to one control and unmutes it.
func (m *Mixer) Set(ctx context.Context, cardIndex int, control string, percent int) error {
	if percent < 0 || percent > 100 {
		return fmt.Errorf("level %d is outside 0..100", percent)
	}
	_, err := m.run(ctx, "-c", strconv.Itoa(cardIndex), "sset", control, strconv.Itoa(percent)+"%", "unmute")
	return err
}
