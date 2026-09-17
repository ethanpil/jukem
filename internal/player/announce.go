package player

import (
	"fmt"
	"strconv"

	"github.com/fhs/gompd/v2/mpd"
)

// InsertNext adds one file to the queue and returns its queue id. With
// afterID it goes after that entry, otherwise after the current entry.
// An announcement uses it: the file plays instead of the music, and the
// music continues afterwards. A group of announcements gives the id of
// the entry before, so the group stays together and in its order.
func (p *Player) InsertNext(file string, afterID int) (int, error) {
	var id int
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		if st := parseStatus(attrs); st.QueueLength >= MaxQueue {
			return fmt.Errorf("the queue is full (%d entries)", st.QueueLength)
		}
		// The position is read now, because an entry before it can be
		// gone since the caller saw the queue.
		var a mpd.Attrs
		switch {
		case afterID > 0:
			found, err := c.Command("playlistid %d", afterID).Attrs()
			if err != nil {
				return err
			}
			pos, err := strconv.Atoi(found["Pos"])
			if err != nil {
				return fmt.Errorf("the entry before is no longer in the queue: %w", err)
			}
			a, err = c.Command("addid %s %d", literal(file), pos+1).Attrs()
			if err != nil {
				return err
			}
		case attrs["song"] != "":
			// A current entry exists while playing, paused, or stopped
			// inside the queue. The insert goes after it.
			a, err = c.Command("addid %s +0", literal(file)).Attrs()
			if err != nil {
				return err
			}
		default:
			a, err = c.Command("addid %s 0", literal(file)).Attrs()
			if err != nil {
				return err
			}
		}
		id, err = strconv.Atoi(a["Id"])
		return err
	})
	return id, err
}
