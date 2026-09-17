package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/library"
	"jukem/internal/scheduler"
	"jukem/internal/store"
)

// validateSource checks a schedule source and returns it in the form
// the store compares: a clean folder path, or a playlist id with no
// extra characters.
func (s *Server) validateSource(ctx context.Context, typ, ref string) (string, error) {
	switch typ {
	case "directory":
		clean, err := library.CleanRel(ref)
		if err != nil {
			return "", huma.Error422UnprocessableEntity("the source folder path is not allowed")
		}
		return clean, nil
	case "playlist":
		id, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
		if err != nil || id <= 0 {
			return "", huma.Error422UnprocessableEntity("the playlist reference must be an id")
		}
		if _, ok, err := s.store.GetPlaylist(ctx, id); err != nil {
			return "", err
		} else if !ok {
			return "", huma.Error422UnprocessableEntity("no such playlist")
		}
		return strconv.FormatInt(id, 10), nil
	case "stream":
		ref = strings.TrimSpace(ref)
		u, err := url.Parse(ref)
		if err != nil || u.Host == "" {
			return "", huma.Error422UnprocessableEntity("the stream address is not an address")
		}
		switch u.Scheme {
		case "http", "https":
		default:
			return "", huma.Error422UnprocessableEntity("the stream address must start with http:// or https://")
		}
		return ref, nil
	}
	return "", huma.Error422UnprocessableEntity("source_type must be directory, playlist or stream")
}

func (s *Server) loc() *time.Location {
	set := s.opts.Settings()
	return set.Location()
}

// checkConflicts reports overlaps of r with the other enabled rules.
func (s *Server) checkConflicts(ctx context.Context, r store.Schedule) error {
	if !r.Enabled {
		return nil
	}
	rules, err := s.store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	all := []store.Schedule{r}
	for _, o := range rules {
		if o.ID != r.ID {
			all = append(all, o)
		}
	}
	var mine []scheduler.Conflict
	for _, c := range scheduler.Conflicts(all, s.loc(), s.opts.Scheduler.Now()) {
		if c.RuleID == r.ID || c.OtherID == r.ID {
			mine = append(mine, c)
		}
	}
	if len(mine) == 0 {
		return nil
	}
	names := ""
	for i, c := range mine {
		other := c.OtherName
		if c.OtherID == r.ID {
			other = c.RuleName
		}
		if i > 0 {
			names += ", "
		}
		names += fmt.Sprintf("%s (%s)", other, c.At)
	}
	return huma.Error409Conflict("the rule overlaps with " + names)
}

// saveRule validates a rule, checks conflicts, and creates or replaces it.
func (s *Server) saveRule(ctx context.Context, r store.Schedule) (*struct{ Body store.Schedule }, error) {
	ref, err := s.validateSource(ctx, r.SourceType, r.SourceRef)
	if err != nil {
		return nil, err
	}
	r.SourceRef = ref
	// A stream is one address that plays until the window ends, so there is
	// nothing to shuffle.
	if r.SourceType == "stream" {
		r.Shuffle = false
	}
	if err := s.checkConflicts(ctx, r); err != nil {
		return nil, err
	}
	if r.ID == 0 {
		id, err := s.store.CreateSchedule(ctx, r)
		if err != nil {
			return nil, err
		}
		r.ID = id
	} else {
		ok, err := s.store.UpdateSchedule(ctx, r)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no such rule")
		}
	}
	s.opts.Scheduler.Invalidate()
	return &struct{ Body store.Schedule }{Body: r}, nil
}

func (s *Server) registerSchedule(api huma.API) {
	type idInput struct {
		ID int64 `path:"id"`
	}
	type nextAnnouncement struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	type nextOutput struct {
		Body struct {
			At            *time.Time         `json:"at" doc:"The next play time, or null when none is in the coming week"`
			Announcements []nextAnnouncement `json:"announcements" doc:"The announcements that play at that time, in their play order"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "next-announcements", Method: http.MethodGet, Path: "/announcements/next", Tags: []string{"schedule"},
		Summary: "The next announcement time",
	}, func(ctx context.Context, _ *struct{}) (*nextOutput, error) {
		list, err := s.store.ListAnnouncements(ctx)
		if err != nil {
			return nil, err
		}
		out := &nextOutput{}
		out.Body.Announcements = []nextAnnouncement{}
		if at, group, ok := scheduler.NextAnnouncements(list, s.opts.Scheduler.Now(), s.loc()); ok {
			out.Body.At = &at
			for _, a := range group {
				out.Body.Announcements = append(out.Body.Announcements, nextAnnouncement{ID: a.ID, Name: a.Name})
			}
		}
		return out, nil
	})

	type rulesOutput struct {
		Body struct {
			Schedules []store.Schedule     `json:"schedules"`
			Conflicts []scheduler.Conflict `json:"conflicts" doc:"Overlaps among the enabled rules"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-schedules", Method: http.MethodGet, Path: "/schedules", Tags: []string{"schedule"},
		Summary: "Weekly rules with their conflicts",
	}, func(ctx context.Context, _ *struct{}) (*rulesOutput, error) {
		rules, err := s.store.ListSchedules(ctx)
		if err != nil {
			return nil, err
		}
		out := &rulesOutput{}
		out.Body.Schedules = rules
		out.Body.Conflicts = scheduler.Conflicts(rules, s.loc(), s.opts.Scheduler.Now())
		if out.Body.Conflicts == nil {
			out.Body.Conflicts = []scheduler.Conflict{}
		}
		return out, nil
	})

	type ruleInput struct {
		Body store.Schedule
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-schedule", Method: http.MethodPost, Path: "/schedules", Tags: []string{"schedule"},
		Summary: "Create a rule", Description: "Fails with 409 when the rule overlaps another enabled rule.", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *ruleInput) (*struct{ Body store.Schedule }, error) {
		r := in.Body
		r.ID = 0
		return s.saveRule(ctx, r)
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-schedule", Method: http.MethodGet, Path: "/schedules/{id}", Tags: []string{"schedule"},
		Summary: "One rule",
	}, func(ctx context.Context, in *idInput) (*struct{ Body store.Schedule }, error) {
		r, ok, err := s.store.GetSchedule(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no such rule")
		}
		return &struct{ Body store.Schedule }{Body: r}, nil
	})

	type updateRuleInput struct {
		ID   int64 `path:"id"`
		Body store.Schedule
	}
	huma.Register(api, huma.Operation{
		OperationID: "update-schedule", Method: http.MethodPut, Path: "/schedules/{id}", Tags: []string{"schedule"},
		Summary: "Replace a rule", Description: "Fails with 409 when the rule overlaps another enabled rule.",
	}, func(ctx context.Context, in *updateRuleInput) (*struct{ Body store.Schedule }, error) {
		r := in.Body
		r.ID = in.ID
		return s.saveRule(ctx, r)
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-schedule", Method: http.MethodDelete, Path: "/schedules/{id}", Tags: []string{"schedule"},
		Summary: "Delete a rule", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		ok, err := s.store.DeleteSchedule(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no such rule")
		}
		s.opts.Scheduler.Invalidate()
		return nil, nil
	})

	type intervalsInput struct {
		From time.Time `query:"from" doc:"Start of the range, RFC 3339"`
		To   time.Time `query:"to" doc:"End of the range, RFC 3339"`
		Week string    `query:"week" doc:"A date YYYY-MM-DD in the configured zone; the range is that day and the six after it, on local day boundaries"`
	}
	type intervalsOutput struct {
		Body struct {
			Intervals []scheduler.Interval `json:"intervals"`
			TimeZone  string               `json:"time_zone"`
			From      time.Time            `json:"from"`
			To        time.Time            `json:"to"`
			// Days lists the local midnight of each day in the range, so a
			// client draws day columns without its own zone arithmetic.
			Days []time.Time `json:"days"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "schedule-intervals", Method: http.MethodGet, Path: "/schedules/intervals", Tags: []string{"schedule"},
		Summary: "Expanded intervals for a date range, what the week view draws",
	}, func(ctx context.Context, in *intervalsInput) (*intervalsOutput, error) {
		from, to := in.From, in.To
		loc := s.loc()
		if in.Week != "" {
			day, err := time.ParseInLocation("2006-01-02", in.Week, loc)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("week must be YYYY-MM-DD")
			}
			from = day
			to = day.AddDate(0, 0, 7)
		}
		if from.IsZero() {
			from = s.opts.Scheduler.Now()
		}
		if to.IsZero() {
			to = from.AddDate(0, 0, 7)
		}
		if to.Sub(from) > 62*24*time.Hour {
			return nil, huma.Error422UnprocessableEntity("the range is longer than 62 days")
		}
		ivs, err := s.opts.Scheduler.Intervals(ctx, from, to)
		if err != nil {
			return nil, err
		}
		out := &intervalsOutput{}
		out.Body.Intervals = ivs
		if out.Body.Intervals == nil {
			out.Body.Intervals = []scheduler.Interval{}
		}
		out.Body.TimeZone = s.opts.Settings().TimeZone
		out.Body.From, out.Body.To = from, to
		out.Body.Days = []time.Time{}
		y, m, d := from.In(loc).Date()
		for i := 0; ; i++ {
			day := time.Date(y, m, d+i, 0, 0, 0, 0, loc)
			if !day.Before(to) {
				break
			}
			out.Body.Days = append(out.Body.Days, day)
		}
		return out, nil
	})

	// Exceptions
	type excListInput struct {
		From string `query:"from" doc:"YYYY-MM-DD"`
		To   string `query:"to" doc:"YYYY-MM-DD"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-exceptions", Method: http.MethodGet, Path: "/schedule-exceptions", Tags: []string{"schedule"},
		Summary: "Date exceptions",
	}, func(ctx context.Context, in *excListInput) (*struct {
		Body struct {
			Exceptions []store.Exception `json:"exceptions"`
		}
	}, error) {
		list, err := s.store.ListExceptions(ctx, in.From, in.To)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Exceptions []store.Exception `json:"exceptions"`
			}
		}{}
		out.Body.Exceptions = list
		return out, nil
	})

	type excInput struct {
		Body store.Exception
	}
	putException := func(ctx context.Context, e store.Exception) error {
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			return huma.Error422UnprocessableEntity("date must be YYYY-MM-DD")
		}
		switch e.Kind {
		case "hours":
			if e.StartTime == nil || e.EndTime == nil || e.SourceType == nil || e.SourceRef == nil {
				return huma.Error422UnprocessableEntity("hours needs start_time, end_time, source_type and source_ref")
			}
		case "source":
			if e.SourceType == nil || e.SourceRef == nil {
				return huma.Error422UnprocessableEntity("source needs source_type and source_ref")
			}
		}
		if e.SourceType != nil {
			if e.SourceRef == nil {
				return huma.Error422UnprocessableEntity("source_type needs source_ref")
			}
			ref, err := s.validateSource(ctx, *e.SourceType, *e.SourceRef)
			if err != nil {
				return err
			}
			e.SourceRef = &ref
		}
		if err := s.store.PutException(ctx, e); err != nil {
			return err
		}
		s.opts.Scheduler.Invalidate()
		return nil
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-exception", Method: http.MethodPost, Path: "/schedule-exceptions", Tags: []string{"schedule"},
		Summary: "Create or replace the exception for a date", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *excInput) (*struct{ Body store.Exception }, error) {
		if in.Body.Date == "" {
			return nil, huma.Error422UnprocessableEntity("date is required")
		}
		if err := putException(ctx, in.Body); err != nil {
			return nil, err
		}
		return &struct{ Body store.Exception }{Body: in.Body}, nil
	})
	type excDateInput struct {
		Date string `path:"date"`
		Body store.Exception
	}
	huma.Register(api, huma.Operation{
		OperationID: "update-exception", Method: http.MethodPut, Path: "/schedule-exceptions/{date}", Tags: []string{"schedule"},
		Summary: "Replace the exception for a date",
	}, func(ctx context.Context, in *excDateInput) (*struct{ Body store.Exception }, error) {
		e := in.Body
		e.Date = in.Date
		if err := putException(ctx, e); err != nil {
			return nil, err
		}
		return &struct{ Body store.Exception }{Body: e}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-exception", Method: http.MethodDelete, Path: "/schedule-exceptions/{date}", Tags: []string{"schedule"},
		Summary: "Remove the exception for a date", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		Date string `path:"date"`
	}) (*struct{}, error) {
		ok, err := s.store.DeleteException(ctx, in.Date)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no exception on that date")
		}
		s.opts.Scheduler.Invalidate()
		return nil, nil
	})

	// Override
	type overrideOutput struct {
		Body struct {
			Active   bool            `json:"active"`
			Override *store.Override `json:"override,omitempty"`
			EndsAt   *time.Time      `json:"ends_at,omitempty" doc:"When the schedule resumes, if known"`
		}
	}
	readOverride := func(ctx context.Context) *overrideOutput {
		out := &overrideOutput{}
		if o, end, ok := s.opts.Scheduler.Override(ctx); ok {
			out.Body.Active = true
			out.Body.Override = &o
			out.Body.EndsAt = end
		}
		return out
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-override", Method: http.MethodGet, Path: "/override", Tags: []string{"schedule"},
		Summary: "The active override",
	}, func(ctx context.Context, in *zoneInput) (*overrideOutput, error) {
		return readOverride(ctx), nil
	})
	type overrideInput struct {
		zoneInput
		Body struct {
			Mode    string `json:"mode" enum:"until_next,timed" doc:"until_next ends at the next scheduled event; timed ends after minutes or at the next event, whichever is first"`
			Minutes int    `json:"minutes,omitempty" minimum:"1" maximum:"1440"`
			Intent  string `json:"intent,omitempty" enum:"play,pause,stop" doc:"Defaults to the current state"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-override", Method: http.MethodPost, Path: "/override", Tags: []string{"schedule"},
		Summary: "Hold play, pause or stop until the schedule resumes", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *overrideInput) (*overrideOutput, error) {
		if !s.opts.Settings().SchedulerEnabled {
			return nil, huma.Error409Conflict("the scheduler is off; everything is manual")
		}
		p, _ := PrincipalFrom(ctx)
		o := store.Override{Mode: in.Body.Mode, Intent: in.Body.Intent, Source: p.Source()}
		if o.Intent == "" {
			st, err := s.opts.Player.Status()
			if err != nil {
				return nil, mpdError(err)
			}
			o.Intent = "pause"
			if st.State == "play" {
				o.Intent = "play"
			}
		}
		if o.Mode == "timed" {
			if in.Body.Minutes <= 0 {
				return nil, huma.Error422UnprocessableEntity("a timed override needs minutes")
			}
			end := s.opts.Scheduler.Now().Add(time.Duration(in.Body.Minutes) * time.Minute)
			o.EndsAt = &end
		}
		if err := s.opts.Scheduler.CreateOverride(ctx, o); err != nil {
			return nil, err
		}
		return readOverride(ctx), nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "clear-override", Method: http.MethodDelete, Path: "/override", Tags: []string{"schedule"},
		Summary: "Resume the schedule", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *zoneInput) (*struct{}, error) {
		return nil, s.opts.Scheduler.ClearOverride(ctx)
	})

	// Scheduler switch
	type switchOutput struct {
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-scheduler", Method: http.MethodGet, Path: "/scheduler", Tags: []string{"schedule"},
		Summary: "Whether the scheduler is on",
	}, func(ctx context.Context, _ *struct{}) (*switchOutput, error) {
		out := &switchOutput{}
		out.Body.Enabled = s.opts.Settings().SchedulerEnabled
		return out, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "set-scheduler", Method: http.MethodPut, Path: "/scheduler", Tags: []string{"schedule"},
		Summary: "Switch the scheduler on or off", Description: "Off puts the appliance in manual mode and clears any override.",
	}, func(ctx context.Context, in *struct {
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}) (*switchOutput, error) {
		set := s.opts.Settings()
		set.SchedulerEnabled = in.Body.Enabled
		if err := s.opts.UpdateSettings(ctx, set); err != nil {
			return nil, err
		}
		out := &switchOutput{}
		out.Body.Enabled = in.Body.Enabled
		return out, nil
	})

	// Clock
	huma.Register(api, huma.Operation{
		OperationID: "get-clock", Method: http.MethodGet, Path: "/clock", Tags: []string{"schedule"},
		Summary: "Clock source and status",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body scheduler.ClockStatus }, error) {
		return &struct{ Body scheduler.ClockStatus }{Body: s.opts.Clock.Status(ctx, s.opts.Settings().TimeZone)}, nil
	})
	type clockInput struct {
		Body struct {
			Date     string `json:"date" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
			Time     string `json:"time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
			TimeZone string `json:"time_zone" minLength:"1"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "set-clock", Method: http.MethodPut, Path: "/clock", Tags: []string{"schedule"},
		Summary:     "Set the date, time and zone by hand for this boot",
		Description: "jukem runs unprivileged and cannot set the system clock. It stores the difference and applies it to every schedule until the kernel reports synchronisation or the machine reboots.",
	}, func(ctx context.Context, in *clockInput) (*struct{ Body scheduler.ClockStatus }, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		loc, err := time.LoadLocation(in.Body.TimeZone)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("unknown time zone")
		}
		entered, err := time.ParseInLocation("2006-01-02 15:04", in.Body.Date+" "+in.Body.Time, loc)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("date or time is not valid")
		}
		if err := s.opts.Clock.SetManual(ctx, entered); err != nil {
			return nil, err
		}
		set := s.opts.Settings()
		if set.TimeZone != in.Body.TimeZone {
			set.TimeZone = in.Body.TimeZone
			if err := s.opts.UpdateSettings(ctx, set); err != nil {
				return nil, err
			}
		}
		s.opts.Scheduler.Invalidate()
		return &struct{ Body scheduler.ClockStatus }{Body: s.opts.Clock.Status(ctx, in.Body.TimeZone)}, nil
	})
}
