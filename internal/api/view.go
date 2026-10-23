package api

import (
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/host"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/policy"
)

// ViewResponse is the answer of GET /view. Without OpenRC, Present is false and there are no services. Mode is ""
// when it cannot be read. Job is the newest job while it is running or interrupted, and is left out otherwise: a job
// that failed or is done shows in the answer to the request that watches it.
type ViewResponse struct {
	Present  bool          `json:"present"`
	Mode     host.Mode     `json:"mode"`
	Services []ServiceView `json:"services"`
	Counts   Counts        `json:"counts"`
	Job      *jobs.State   `json:"job,omitempty"`
}

// ServiceView is a service as the page shows it: its state, in which a stopped service whose start failed is failed
// (see failedStarts), its protection class, what the policy says to each action on it alone, AutostartIdle when it
// is in the autostart (default or boot) but stopped (neither crashed nor failed), and FailedAt, in UTC, when its start
// failed (a failed state that is derived, and only when the time is known).
type ServiceView struct {
	openrc.Service
	Class         policy.Class                     `json:"class"`
	Allowed       map[policy.Action]policy.Verdict `json:"allowed"`
	AutostartIdle bool                             `json:"autostartIdle"`
	FailedAt      *time.Time                       `json:"failedAt,omitempty"`
}

// Counts are the numbers of the head: all services, those that run (started, one-shot ones too) and the problems
// (crashed or failed).
type Counts struct {
	Total    int `json:"total"`
	Running  int `json:"running"`
	Problems int `json:"problems"`
}

// Details is the answer of GET /services/:name: the service as the view shows it, and its relations.
type Details struct {
	ServiceView
	openrc.Details
}

// view answers GET /view, for everybody. A mode or a newest job that cannot be read is logged, and the page shows the
// services all the same.
func (a *API) view(c fiber.Ctx) error {
	snap, err := a.reader().Read(ctxOf(c))
	if err != nil {
		return toHTTP(fmt.Errorf("reading the services: %w", err))
	}
	mode, err := host.FS{Root: a.Root}.Mode()
	if err != nil {
		slog.Warn("openrc: the system mode cannot be read", "err", err)
		mode = ""
	}
	services := a.views(snap.Services)
	resp := ViewResponse{Present: snap.Present, Mode: mode, Services: services, Counts: count(services)}
	newest, err := a.Jobs.Newest()
	if err != nil {
		slog.Warn("openrc: the newest job cannot be read", "err", err)
	} else if newest != nil && (newest.Status == jobs.Running || newest.Status == jobs.Interrupted) {
		resp.Job = newest
	}
	return c.JSON(resp)
}

// details answers GET /services/:name, for everybody: rc-service <name> ineed, iuse, iafter and needsme, run for a name
// of a fresh list.
func (a *API) details(c fiber.Ctx) error {
	name, err := serviceName(c)
	if err != nil {
		return err
	}
	r := a.reader()
	s, err := readService(ctxOf(c), r, name)
	if err != nil {
		return err
	}
	d, err := r.Details(ctxOf(c), name)
	if err != nil {
		return toHTTP(fmt.Errorf("reading the relations of %s: %w", name, err))
	}
	return c.JSON(Details{ServiceView: a.views([]openrc.Service{s})[0], Details: d})
}

// views are the services as the page shows them (see ServiceView), in their order.
func (a *API) views(services []openrc.Service) []ServiceView {
	failed := a.failedStarts(services)
	views := make([]ServiceView, 0, len(services))
	for _, s := range services {
		at, isFailed := failed[s.Name]
		if isFailed {
			s.State = openrc.Failed
		}
		v := ServiceView{Service: s, Class: policy.ClassOf(s), Allowed: policy.Allowed(s),
			AutostartIdle: s.State == openrc.Stopped && inAutostart(s)}
		if !at.IsZero() {
			v.FailedAt = &at
		}
		views = append(views, v)
	}
	return views
}

// inAutostart reports whether the service is in default or boot.
func inAutostart(s openrc.Service) bool {
	return slices.Contains(s.Runlevels, "default") || slices.Contains(s.Runlevels, "boot")
}

// failedStarts are the stopped services whose start failed, each with the time it failed in UTC, or the zero time
// when that is not known. OpenRC never marks a start that failed (the service is stopped then), so it is derived: the
// runner of the jobs left a failed marker for it (its last start through the page failed), or the system log has a
// failed start of it since the boot that is newer than its ok marker, or it has none (the start at the boot failed, and
// no start or stop through the page worked since). The time is the later of the two that apply; the time of a line of
// the system log only when it is not before the boot time: a host without a real-time clock logs the start of its
// boot with a clock that NTP sets later, and such a time tells nothing. Any other state stays as OpenRC tells it.
// Markers, a boot time or a system log that cannot be read count as none, and the reason is logged: the page never
// fails because of them. The scan of the system log is kept while it stays as it is (see bootFailures).
func (a *API) failedStarts(services []openrc.Service) map[string]time.Time {
	if !slices.ContainsFunc(services, func(s openrc.Service) bool { return s.State == openrc.Stopped }) {
		return nil
	}
	marked, ok, err := jobs.ReadMarks(a.Root, a.MarkDir)
	if err != nil {
		slog.Warn("openrc: the markers of the jobs cannot be read", "err", err)
		marked, ok = nil, nil
	}
	now := a.now()
	var atBoot map[string]time.Time
	boot, err := (host.FS{Root: a.Root}).BootTime(now)
	if err != nil {
		slog.Warn("openrc: the boot time cannot be read", "err", err)
	} else if atBoot, err = a.bootFailures(boot, now); err != nil {
		slog.Warn("openrc: the failed starts since the boot cannot be read", "err", err)
		atBoot = nil
	}
	failed := map[string]time.Time{}
	for _, s := range services {
		if s.State != openrc.Stopped {
			continue
		}
		markedAt, isMarked := marked[s.Name]
		bootAt, failedAtBoot := atBoot[s.Name]
		okAt, hasOK := ok[s.Name]
		failedAtBoot = failedAtBoot && (!hasOK || bootAt.After(okAt))
		if !isMarked && !failedAtBoot {
			continue
		}
		var at time.Time
		if isMarked {
			at = markedAt
		}
		if failedAtBoot && !bootAt.Before(boot.Truncate(time.Second)) && bootAt.After(at) {
			at = bootAt
		}
		if !at.IsZero() {
			at = at.UTC()
		}
		failed[s.Name] = at
	}
	return failed
}

// count are the counts of the services.
func count(services []ServiceView) Counts {
	c := Counts{Total: len(services)}
	for _, s := range services {
		switch s.State {
		case openrc.Started:
			c.Running++
		case openrc.Crashed, openrc.Failed:
			c.Problems++
		}
	}
	return c
}
