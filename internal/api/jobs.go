package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/actions"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/host"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/policy"
	"github.com/Pukka922/edelweiss/sdk"
)

// createJob answers POST /jobs (admin mode): it plans the change on a fresh read of the services and starts it as a
// job, 202 with its state. It holds the job lock from the start, so that nothing else changes a service between the
// check and the job, hands it on to the runner and lets go of its own copy in every case. A job whose runner could not
// be started is ended as failed before the lock is released, so that it does not read as interrupted.
func (a *API) createJob(c fiber.Ctx) error {
	var req actions.Request
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return sdk.NewError(fiber.StatusBadRequest, codeInvalidJSON, "the request body must be valid JSON")
	}
	lock, err := a.Jobs.TryLock()
	switch {
	case errors.Is(err, jobs.ErrBusy):
		return sdk.NewError(fiber.StatusConflict, codeBusy, "another change is running; try again")
	case err != nil:
		return toHTTP(fmt.Errorf("taking the job lock: %w", err))
	}
	defer lock.Close()
	plan, err := a.plan(ctxOf(c), req)
	if err != nil {
		return toHTTP(err)
	}
	dir, st, err := a.Jobs.Create(lock, jobs.Spec{Kind: plan.Kind, Target: plan.Target, Steps: plan.Steps}, a.now())
	if err != nil {
		return toHTTP(fmt.Errorf("creating the job: %w", err))
	}
	if err := a.Launcher.Launch(dir, lock); err != nil {
		msg := "the job could not be started: " + err.Error()
		if ferr := a.Jobs.Fail(dir, actions.CodeFailed, msg, a.now()); ferr != nil {
			slog.Error("openrc: the job that could not be started cannot be ended", "job", st.ID, "err", ferr)
		}
		return toHTTP(errors.New(msg))
	}
	return c.Status(fiber.StatusAccepted).JSON(st)
}

// plan reads the services and the host afresh and plans the request on them. The host is checked first: its mode has
// to be known (a host that runs from RAM taken for one installed to disk would lose a change of the autostart without
// lbu commit), a change of the autostart (enable, disable) needs lbu in diskless and data mode, and OpenRC has to run,
// with rc-service and rc-update. The services that stop along are those that need the target in the same read.
func (a *API) plan(ctx context.Context, req actions.Request) (actions.Plan, error) {
	r := a.reader()
	snap, err := r.Read(ctx)
	if err != nil {
		return actions.Plan{}, fmt.Errorf("reading the services: %w", err)
	}
	mode, err := host.FS{Root: a.Root}.Mode()
	autostart := req.Kind == string(policy.Enable) || req.Kind == string(policy.Disable)
	switch {
	case err != nil:
		return actions.Plan{}, fmt.Errorf("reading the system mode: %w", err)
	case autostart && (mode == host.Diskless || mode == host.Data) && r.Tools.Lbu == "":
		return actions.Plan{}, fmt.Errorf("lbu is not installed, which saves a change of the autostart in %s mode", mode)
	case !snap.Present:
		return actions.Plan{}, &actions.Error{Code: actions.CodeNoOpenRC, Message: "OpenRC is not running on this system"}
	case r.Tools.RcService == "" || r.Tools.RcUpdate == "":
		return actions.Plan{}, &actions.Error{Code: actions.CodeNoOpenRC,
			Message: "rc-service or rc-update of OpenRC is not installed"}
	}
	return actions.Build(ctx, snap, r.Dependents, req, actions.Env{Mode: mode, Tools: r.Tools})
}

// job answers GET /jobs/:id, for everybody. An id that is not of the form of a job id is no job.
func (a *API) job(c fiber.Ctx) error {
	st, err := a.Jobs.Get(c.Params("id"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return notFound("there is no such job")
	case err != nil:
		return toHTTP(fmt.Errorf("reading the job: %w", err))
	}
	return c.JSON(st)
}
