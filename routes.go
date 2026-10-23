package main

import (
	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/api"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// register adds the extension's routes for the real host.
func register(r fiber.Router) { api.Register(r, hostAPI()) }

// hostAPI is the API of the real host. Without admin mode the process runs as the user: it reads what it may (the
// services, the logs it may read) and changes nothing; in admin mode, as root, it also plans the changes. Its commands
// run in the environment of OpenRC, with the tools looked up afresh for each request. The launcher starts the runner of
// a job from /proc/self/exe: the binary this process runs from, also after an extension update renamed a new one into
// its place. The view reads the markers of the runner where runnerConfig writes them.
func hostAPI() *api.API {
	return &api.API{
		Root:     "/",
		Runner:   run.Exec{Env: openrc.Env},
		Tools:    func() openrc.Tools { return openrc.FindTools("/") },
		Jobs:     jobs.DefaultStore(),
		Launcher: jobs.ExecLauncher{Binary: "/proc/self/exe", Env: openrc.Env},
		MarkDir:  jobs.DefaultMarkDir,
	}
}

// runnerConfig is the configuration of the detached runner (--run-job): the real host, root only. Its commands run in
// the environment of OpenRC, and it marks the outcome of the starts and stops where the view reads it (hostAPI).
func runnerConfig() jobs.RunnerConfig {
	return jobs.RunnerConfig{
		Store:       jobs.DefaultStore(),
		Exec:        jobs.Executor{Root: "/", Runner: run.Exec{Env: openrc.Env}, MarkDir: jobs.DefaultMarkDir},
		RequireRoot: true,
	}
}
