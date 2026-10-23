// Package api serves the extension's HTTP API: the services as the page shows them, their details and logs for
// everybody, and the changes in admin mode only. A change is planned by internal/actions on a fresh read of the
// services, and runs as a job (internal/jobs) in a detached runner, so that stopping the extension never stops it
// halfway.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/actions"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
	"github.com/Pukka922/edelweiss/sdk"
)

// API holds the dependencies of the routes. Root is the root of the system ("/" on the host): the services, the system
// mode, the logs and the markers that the runner of the jobs leaves in MarkDir (see jobs.Executor) are read below it.
// Runner runs the OpenRC tools, which Tools looks up afresh for each request (a package installed meanwhile has its
// tools then). Now is the clock, time.Now when nil. An API is used by its pointer: it keeps the last scan of the system
// log for the view.
type API struct {
	Root     string
	Runner   run.Runner
	Tools    func() openrc.Tools
	Jobs     jobs.Store
	Launcher jobs.Launcher
	MarkDir  string
	Now      func() time.Time

	// every is how often a log stream looks for new lines; followEvery when 0.
	every time.Duration
	// boot keeps the last scan for the failed starts since the boot (see bootFailures), which scanBoot makes
	// (logs.BootFailures when nil).
	boot     bootCache
	scanBoot func(root string, bootTime, now time.Time) (map[string]time.Time, bool, error)
}

// followEvery is how often a log stream looks for new lines.
const followEvery = time.Second

// Register adds the routes to r.
func Register(r fiber.Router, a *API) {
	r.Get("/view", a.view)
	r.Get("/services/:name", a.details)
	r.Get("/services/:name/logs", a.logLines)
	r.Get("/services/:name/logs/stream", a.prepareStream, logStream(a))
	r.Post("/jobs", sdk.RequireAdmin(), a.createJob)
	r.Get("/jobs/:id", a.job)
}

// The codes of the answers that internal/actions has no code for.
const (
	codeInvalidJSON = "invalid_json"
	codeBusy        = "busy"
)

// statusOf maps the codes of internal/actions to HTTP; every other refusal is a conflict (409).
var statusOf = map[string]int{
	actions.CodeInvalid:  fiber.StatusBadRequest,
	actions.CodeNotFound: fiber.StatusNotFound,
	actions.CodeFailed:   fiber.StatusInternalServerError,
}

// toHTTP turns an error into the SDK's error envelope. A refusal of internal/actions keeps its code and brings what the
// UI words it by: the field of an invalid input, the reason (the protection class) and the protected service of a
// protected one, the reason and the running services that stop along of one that needs the confirmation. Any other
// error is a failure of the host (500 failed).
func toHTTP(err error) error {
	var e *actions.Error
	if !errors.As(err, &e) {
		return sdk.NewError(fiber.StatusInternalServerError, actions.CodeFailed, err.Error())
	}
	status, ok := statusOf[e.Code]
	if !ok {
		status = fiber.StatusConflict
	}
	apiErr := sdk.NewError(status, e.Code, e.Message)
	switch e.Code {
	case actions.CodeInvalid:
		if e.Field != "" {
			apiErr = apiErr.WithDetails(map[string]any{"field": e.Field})
		}
	case actions.CodeProtected:
		apiErr = apiErr.WithDetails(map[string]any{"reason": e.Reason, "service": e.Service})
	case actions.CodeConfirm:
		services := e.Services
		if services == nil {
			services = []string{}
		}
		apiErr = apiErr.WithDetails(map[string]any{"reason": e.Reason, "services": services})
	}
	return apiErr
}

// invalid is the answer to an invalid input, the field of the request it is in.
func invalid(field, format string, a ...any) error {
	return toHTTP(&actions.Error{Code: actions.CodeInvalid, Field: field, Message: fmt.Sprintf(format, a...)})
}

// notFound is the answer for something that is not there.
func notFound(format string, a ...any) error {
	return toHTTP(&actions.Error{Code: actions.CodeNotFound, Message: fmt.Sprintf(format, a...)})
}

// ctxOf is the context a request runs under. With Fiber v3.5, c.Context() is context.Background() unless one is set,
// and fasthttp gives no signal when the client leaves: a command is bounded by its own time limit. Do not use
// c.RequestCtx() instead: it ends only at the shutdown of the server.
func ctxOf(c fiber.Ctx) context.Context { return c.Context() }

func (a *API) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

func (a *API) followEvery() time.Duration {
	if a.every == 0 {
		return followEvery
	}
	return a.every
}

// reader reads the services of the host with the tools there are now.
func (a *API) reader() openrc.Reader {
	return openrc.Reader{Root: a.Root, Runner: a.Runner, Tools: a.Tools()}
}

// serviceName is the name of the service in the path, decoded once: it has to be a valid name (no "/", no "..").
func serviceName(c fiber.Ctx) (string, error) {
	raw := c.Params("name")
	name, err := url.PathUnescape(raw)
	if err != nil || !openrc.ValidName(name) {
		return "", invalid("service", "%q is not the name of a service", raw)
	}
	return strings.Clone(name), nil // not the buffer of the request, which fasthttp reuses
}

// readService reads the services afresh with r and returns the one of that name: not_found when they do not hold it
// (also when there is no OpenRC). A name reaches nothing that is not in the list.
func readService(ctx context.Context, r openrc.Reader, name string) (openrc.Service, error) {
	snap, err := r.Read(ctx)
	if err != nil {
		return openrc.Service{}, toHTTP(fmt.Errorf("reading the services: %w", err))
	}
	i := slices.IndexFunc(snap.Services, func(s openrc.Service) bool { return s.Name == name })
	if i < 0 {
		return openrc.Service{}, notFound("there is no service %s", name)
	}
	return snap.Services[i], nil
}
