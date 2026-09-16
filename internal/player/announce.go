package player

import (
	"strconv"

	"github.com/fhs/gompd/v2/mpd"
)

// InsertNext adds one file directly after the current entry and returns its
// queue id. An announcement uses it: the file plays instead of the music,
// and the music continues afterwards.
func (p *Player) InsertNext(file string) (int, error) {
	var id int
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		// A current entry exists while playing, paused, or stopped inside
		// the queue. The insert goes after it, otherwise on top.
		var a mpd.Attrs
		if attrs["song"] != "" {
			a, err = c.Command("addid %s +0", literal(file)).Attrs()
		} else {
			a, err = c.Command("addid %s 0", literal(file)).Attrs()
		}
		if err != nil {
			return err
		}
		id, err = strconv.Atoi(a["Id"])
		return err
	})
	return id, err
}
