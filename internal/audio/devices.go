// Package audio discovers ALSA playback devices, gives each a stable
// identity, and drives the hardware mixer.
package audio

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Identity is what jukem saves for a selected output. It matches a present
// card by USB serial, by physical port, or by ALSA card ID, in that order.
type Identity struct {
	CardID    string `json:"card_id" doc:"ALSA card ID"`
	Device    int    `json:"device" doc:"ALSA device number on the card"`
	VendorID  string `json:"vendor_id,omitempty" doc:"USB vendor ID"`
	ProductID string `json:"product_id,omitempty" doc:"USB product ID"`
	Serial    string `json:"serial,omitempty" doc:"USB serial number"`
	SysPath   string `json:"sys_path,omitempty" doc:"sysfs device path, which encodes the physical port"`
	Name      string `json:"name,omitempty" doc:"Description shown to people"`
}

// Device is one playback device found by aplay -l.
type Device struct {
	Identity
	CardIndex  int    `json:"card_index" doc:"ALSA card number, which changes between boots"`
	CardName   string `json:"card_name" doc:"Card description from aplay"`
	DeviceName string `json:"device_name" doc:"Device description from aplay"`
	Hidden     bool   `json:"hidden" doc:"Loopback or dummy card, hidden unless show all is on"`
}

// ALSAName is the device string MPD opens. plughw lets ALSA convert sample
// rate, format and channel count.
func (d Device) ALSAName() string {
	return fmt.Sprintf("plughw:CARD=%s,DEV=%d", d.CardID, d.Device)
}

// OutputName names the audio_output block. MPD numbers outputs by config
// position, so the name is what jukem selects by.
func (d Identity) OutputName() string {
	kind := "card"
	if d.VendorID != "" {
		kind = "usb"
	}
	return fmt.Sprintf("%s-%s-%d", kind, d.CardID, d.Device)
}

// Key is a stable key for per-device settings such as the mixer level.
func (d Identity) Key() string {
	dev := strconv.Itoa(d.Device)
	switch {
	case d.VendorID != "" && d.Serial != "":
		return "usb:" + d.VendorID + ":" + d.ProductID + ":" + d.Serial + ":" + dev
	case d.SysPath != "":
		return "path:" + d.SysPath + ":" + dev
	}
	return "card:" + d.CardID + ":" + dev
}

var aplayLine = regexp.MustCompile(`^card (\d+): (\S+) \[(.*?)\], device (\d+): (.*?) \[(.*?)\]`)

// ParseAplay reads the output of LC_ALL=C aplay -l.
func ParseAplay(out string) []Device {
	var devices []Device
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		m := aplayLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		dev, _ := strconv.Atoi(m[4])
		d := Device{
			Identity:   Identity{CardID: m[2], Device: dev, Name: m[3]},
			CardIndex:  idx,
			CardName:   m[3],
			DeviceName: m[6],
		}
		if dev != 0 {
			d.Name = fmt.Sprintf("%s (%s)", m[3], m[6])
		}
		lower := strings.ToLower(m[2] + " " + m[3])
		d.Hidden = strings.Contains(lower, "loopback") || strings.Contains(lower, "dummy")
		devices = append(devices, d)
	}
	return devices
}

// Match finds the present device for a saved identity. It returns the
// device and the rule that matched: "serial", "port", "card_id", or "" when
// nothing matches.
func Match(saved Identity, present []Device) (*Device, string) {
	if saved.Serial != "" && saved.VendorID != "" {
		var hits []int
		for i, d := range present {
			if d.VendorID == saved.VendorID && d.ProductID == saved.ProductID && d.Serial == saved.Serial && d.Device == saved.Device {
				hits = append(hits, i)
			}
		}
		// Cheap DACs often share one serial across every unit, so the rule
		// does not trust a tie.
		if len(hits) == 1 {
			return &present[hits[0]], "serial"
		}
	}
	if saved.SysPath != "" {
		for i, d := range present {
			if d.VendorID == saved.VendorID && d.ProductID == saved.ProductID && d.SysPath == saved.SysPath && d.Device == saved.Device {
				return &present[i], "port"
			}
		}
	}
	for i, d := range present {
		if d.CardID == saved.CardID && d.Device == saved.Device {
			return &present[i], "card_id"
		}
	}
	return nil, ""
}
