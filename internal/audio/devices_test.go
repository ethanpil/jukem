package audio

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const aplayOut = `**** List of PLAYBACK Hardware Devices ****
card 0: vc4hdmi [vc4-hdmi], device 0: MAI PCM i2s-hifi-0 [MAI PCM i2s-hifi-0]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
card 1: Device [USB Audio Device], device 0: USB Audio [USB Audio]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
card 2: Loopback [Loopback], device 0: Loopback PCM [Loopback PCM]
  Subdevices: 8/8
card 2: Loopback [Loopback], device 1: Loopback PCM [Loopback PCM]
`

func TestParseAplay(t *testing.T) {
	devs := ParseAplay(aplayOut)
	if len(devs) != 4 {
		t.Fatalf("got %d devices", len(devs))
	}
	usb := devs[1]
	if usb.CardID != "Device" || usb.CardIndex != 1 || usb.Device != 0 || usb.Name != "USB Audio Device" {
		t.Fatalf("got %+v", usb)
	}
	if usb.ALSAName() != "plughw:CARD=Device,DEV=0" {
		t.Fatal(usb.ALSAName())
	}
	if !devs[2].Hidden || !devs[3].Hidden || devs[0].Hidden {
		t.Fatal("loopback hidden flag")
	}
	if devs[3].Name != "Loopback (Loopback PCM)" {
		t.Fatal(devs[3].Name)
	}
	if ParseAplay("aplay: device_list:274: no soundcards found...") != nil {
		t.Fatal("expected no devices")
	}
}

func TestMatchRules(t *testing.T) {
	a := Device{Identity: Identity{CardID: "Device", VendorID: "0d8c", ProductID: "0014", Serial: "0001", SysPath: "devices/usb1/1-1/1-1:1.0"}}
	b := Device{Identity: Identity{CardID: "Device_1", VendorID: "0d8c", ProductID: "0014", Serial: "0001", SysPath: "devices/usb1/1-2/1-2:1.0"}}
	hdmi := Device{Identity: Identity{CardID: "vc4hdmi", SysPath: "devices/platform/hdmi"}}
	present := []Device{a, b, hdmi}

	// Two cards tie on the serial rule, so the port decides.
	got, rule := Match(Identity{CardID: "old", VendorID: "0d8c", ProductID: "0014", Serial: "0001", SysPath: b.SysPath}, present)
	if got == nil || got.CardID != "Device_1" || rule != "port" {
		t.Fatalf("got %v %s", got, rule)
	}
	// A unique serial wins even when the port and card ID changed.
	present2 := []Device{a, hdmi}
	got, rule = Match(Identity{CardID: "x", VendorID: "0d8c", ProductID: "0014", Serial: "0001", SysPath: "elsewhere"}, present2)
	if got == nil || got.CardID != "Device" || rule != "serial" {
		t.Fatalf("got %v %s", got, rule)
	}
	// Non-USB devices match on the sysfs path.
	got, rule = Match(Identity{CardID: "renamed", SysPath: "devices/platform/hdmi"}, present)
	if got == nil || got.CardID != "vc4hdmi" || rule != "port" {
		t.Fatalf("got %v %s", got, rule)
	}
	// Card ID is the last resort.
	got, rule = Match(Identity{CardID: "vc4hdmi"}, present)
	if got == nil || rule != "card_id" {
		t.Fatalf("got %v %s", got, rule)
	}
	// Device number must match.
	if got, _ = Match(Identity{CardID: "vc4hdmi", Device: 1}, present); got != nil {
		t.Fatal("device number ignored")
	}
	if got, _ = Match(Identity{CardID: "gone"}, present); got != nil {
		t.Fatal("expected no match")
	}
}

func TestEnrichFromSysfs(t *testing.T) {
	root := t.TempDir()
	// Real sysfs names hold colons; NTFS rejects them, so the test uses
	// names without.
	usbDev := filepath.Join(root, "devices", "pci0000", "usb1", "1-1")
	iface := filepath.Join(usbDev, "1-1_1.0", "sound", "card1")
	os.MkdirAll(iface, 0o750)
	os.WriteFile(filepath.Join(usbDev, "idVendor"), []byte("0d8c\n"), 0o600)
	os.WriteFile(filepath.Join(usbDev, "idProduct"), []byte("0014\n"), 0o600)
	os.WriteFile(filepath.Join(usbDev, "serial"), []byte("ABC123\n"), 0o600)
	classDir := filepath.Join(root, "class", "sound", "card1")
	os.MkdirAll(classDir, 0o750)
	// A real sysfs uses a symlink; a directory with the same target works
	// for EvalSymlinks too, and symlinks need privileges on Windows.
	os.MkdirAll(filepath.Join(classDir, "device"), 0o750)
	os.Remove(filepath.Join(classDir, "device"))
	if err := os.Symlink(filepath.Join(usbDev, "1-1_1.0"), filepath.Join(classDir, "device")); err != nil {
		t.Skip("symlinks not available:", err)
	}
	d := Device{CardIndex: 1}
	if !enrich(&d, root) {
		t.Fatal("enrich failed")
	}
	if d.VendorID != "0d8c" || d.ProductID != "0014" || d.Serial != "ABC123" {
		t.Fatalf("got %+v", d)
	}
	if d.SysPath != "devices/pci0000/usb1/1-1/1-1_1.0" {
		t.Fatalf("sys path %q", d.SysPath)
	}
	if d.Key() != "usb:0d8c:0014:ABC123:0" {
		t.Fatal(d.Key())
	}
}

func TestManagerRescanReportsChanges(t *testing.T) {
	var changes []Snapshot
	m := NewManager(slog.New(slog.DiscardHandler))
	m.OnChange = func(s Snapshot) { changes = append(changes, s) }
	m.sysRoot = t.TempDir()
	out := aplayOut
	m.aplay = func(context.Context) (string, error) { return out, nil }
	if err := m.SetSaved(context.Background(), &Identity{CardID: "Device"}); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Selected == nil || changes[0].MatchRule != "card_id" {
		t.Fatalf("first scan: %+v", changes)
	}
	// Nothing changed: no callback.
	m.Rescan(context.Background())
	if len(changes) != 1 {
		t.Fatal("callback on an unchanged scan")
	}
	if m.interval() != 60e9 {
		t.Fatal("interval with device present")
	}
	// Unplug the DAC.
	out = "card 0: vc4hdmi [vc4-hdmi], device 0: MAI PCM i2s-hifi-0 [MAI PCM i2s-hifi-0]\n"
	m.Rescan(context.Background())
	if len(changes) != 2 || changes[1].Selected != nil {
		t.Fatalf("after unplug: %d changes", len(changes))
	}
	if m.interval() != 15e9 {
		t.Fatal("interval while missing")
	}
}

func TestParseMixer(t *testing.T) {
	out := `Simple mixer control 'PCM',0
  Capabilities: pvolume pswitch pswitch-joined
  Playback channels: Front Left - Front Right
  Limits: Playback 0 - 255
  Mono:
  Front Left: Playback 200 [78%] [-3.20dB] [on]
  Front Right: Playback 255 [100%] [0.00dB] [on]
Simple mixer control 'Mic',0
  Capabilities: cvolume cswitch
  Capture channels: Mono
  Mono: Capture 0 [0%] [off]
Simple mixer control 'Speaker',0
  Capabilities: pvolume
  Playback channels: Mono
  Mono: Playback 10 [40%] [-10.00dB]
`
	controls := ParseMixer(out)
	if len(controls) != 2 {
		t.Fatalf("got %+v", controls)
	}
	if controls[0].Name != "PCM" || controls[0].Percent != 78 || controls[0].Muted {
		t.Fatalf("got %+v", controls[0])
	}
	if controls[1].Name != "Speaker" || controls[1].Percent != 40 {
		t.Fatalf("got %+v", controls[1])
	}
}

func TestMixerSetValidates(t *testing.T) {
	var got []string
	m := &Mixer{run: func(ctx context.Context, args ...string) (string, error) { got = args; return "", nil }}
	if err := m.Set(context.Background(), 1, "PCM", 80); err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[3] != "PCM" || got[4] != "80%" || got[5] != "unmute" {
		t.Fatalf("args %v", got)
	}
	if err := m.Set(context.Background(), 1, "PCM", 101); err == nil {
		t.Fatal("expected range error")
	}
}
