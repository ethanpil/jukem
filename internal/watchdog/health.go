// Package watchdog checks that reality agrees with the schedule and reports
// health in plain language.
package watchdog

// Status is the state of one health check.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
)

// Check is one line on the Health screen.
type Check struct {
	Name    string `json:"name" doc:"Component name, for example Audio or Clock"`
	Status  Status `json:"status" enum:"ok,warning,error"`
	Summary string `json:"summary" doc:"One line in plain language"`
	Fix     string `json:"fix,omitempty" doc:"What to do when the status is not ok"`
}

// Report is the full health detail.
type Report struct {
	Status      Status  `json:"status" enum:"ok,warning,error" doc:"Worst status of all checks"`
	Maintenance bool    `json:"maintenance" doc:"True when jukem runs in maintenance mode"`
	Reason      string  `json:"reason,omitempty" doc:"Why jukem is in maintenance mode"`
	Fix         string  `json:"fix,omitempty" doc:"How to leave maintenance mode"`
	Checks      []Check `json:"checks"`
}

// Worst returns the most severe status in the report's checks.
func Worst(checks []Check) Status {
	worst := StatusOK
	for _, c := range checks {
		switch {
		case c.Status == StatusError:
			return StatusError
		case c.Status == StatusWarning:
			worst = StatusWarning
		}
	}
	return worst
}
