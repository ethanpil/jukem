package player

import "time"

// OwnerState says who decides what plays.
type OwnerState string

const (
	// OwnerScheduled means the schedule decides.
	OwnerScheduled OwnerState = "SCHEDULED"
	// OwnerOverridden means a person's action decides, with an end.
	OwnerOverridden OwnerState = "OVERRIDDEN"
	// OwnerManual means the scheduler is off and the person decides.
	OwnerManual OwnerState = "MANUAL"
	// OwnerUnavailable means jukem cannot act: clock, MPD or output.
	OwnerUnavailable OwnerState = "UNAVAILABLE"
)

// Owner is the current owner of playback with its reason in plain words.
type Owner struct {
	State   OwnerState `json:"state" enum:"SCHEDULED,OVERRIDDEN,MANUAL,UNAVAILABLE"`
	Reason  string     `json:"reason" doc:"For example: Morning Mix until 11:00"`
	Program string     `json:"program,omitempty" doc:"Name of the loaded schedule rule or exception"`
	Since   *time.Time `json:"since,omitempty" doc:"When the scheduled window started, if one runs"`
	Until   *time.Time `json:"until,omitempty" doc:"When the current state ends, if known"`
	Warning string     `json:"warning,omitempty" doc:"A problem that does not change the owner, such as an unset clock in manual mode"`
}
