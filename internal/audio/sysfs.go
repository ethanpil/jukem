package audio

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// enrich fills the USB identity and sysfs path of a device from
// <sysRoot>/class/sound/card<N>/device. It returns false when sysfs is not
// readable, in which case only the card ID identifies the device.
func enrich(d *Device, sysRoot string) bool {
	link := filepath.Join(sysRoot, "class", "sound", "card"+strconv.Itoa(d.CardIndex), "device")
	real, err := filepath.EvalSymlinks(link)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(sysRoot, real)
	if err != nil {
		rel = real
	}
	d.SysPath = filepath.ToSlash(rel)
	// USB attributes live on the USB device, which is the interface's
	// parent. Walk up a few levels to find them.
	dir := real
	for i := 0; i < 4; i++ {
		if v := readAttr(dir, "idVendor"); v != "" {
			d.VendorID = v
			d.ProductID = readAttr(dir, "idProduct")
			d.Serial = readAttr(dir, "serial")
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return true
}

func readAttr(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// sysReadable reports whether the sound class directory can be listed.
func sysReadable(sysRoot string) bool {
	_, err := os.ReadDir(filepath.Join(sysRoot, "class", "sound"))
	return err == nil
}
