package player

import (
	"fmt"
	"strconv"

	"github.com/fhs/gompd/v2/mpd"
)

// InsertNext adds one file after the current entry and returns its queue
// id. offset counts the entries already put there, so announcements that
// play back to back stay in their order. An announcement uses it: the file
// plays instead of the music, and the music continues afterwards.
func (p *Player) InsertNext(file string, offset int) (int, error) {
	var id int
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		if st := parseStatus(attrs); st.QueueLength >= MaxQueue {
			return fmt.Errorf("the queue is full (%d entries)", st.QueueLength)
		}
		// A current entry exists while playing, paused, or stopped inside
		// the queue. The insert goes after it, otherwise on top.
		var a mpd.Attrs
		if attrs["song"] != "" {
			a, err = c.Command("addid %s +%d", literal(file), offset).Attrs()
		} else {
			a, err = c.Command("addid %s %d", literal(file), offset).Attrs()
		}
		if err != nil {
			return err
		}
		id, err = strconv.Atoi(a["Id"])
		return err
	})
	return id, err
}
