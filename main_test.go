package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// The test binary acts as the extension's binary when asBinary is set: it runs main, which decides by its arguments what
// to be, as the binary started by the launcher does.
const asBinary = "EDELWEISS_OPENRC_MAIN_TEST_AS_BINARY"

func TestMain(m *testing.M) {
	if os.Getenv(asBinary) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// --run-job <dir> makes the binary the runner of a job and nothing else: it ends with the status of RunJob (2: not a job
// directory) instead of serving the API. The directory here is none, so the test touches nothing of the host.
func TestRunJobEntry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "--run-job", filepath.Join(t.TempDir(), "20261020T120000-abcd"))
	cmd.Env = append(os.Environ(), asBinary+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(stderr.String(), "run-job: ") ||
		!strings.Contains(stderr.String(), "is not a job directory") {
		t.Fatalf("--run-job: %v, stderr %q; want exit status 2 of the runner, saying it is no job directory", err, stderr.String())
	}
}

// The runner works on the real host, as root only, with the commands in the environment of OpenRC.
func TestRunnerConfig(t *testing.T) {
	want := jobs.RunnerConfig{
		Store:       jobs.DefaultStore(),
		Exec:        jobs.Executor{Root: "/", Runner: run.Exec{Env: openrc.Env}, MarkDir: jobs.DefaultMarkDir},
		RequireRoot: true,
	}
	if got := runnerConfig(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runnerConfig() = %+v; want %+v", got, want)
	}
}

// The API reads the host the runner changes: the same root, markers and jobs. Its commands and the runner it launches
// have the environment of OpenRC; the runner is the binary that runs.
func TestHostAPI(t *testing.T) {
	a, cfg := hostAPI(), runnerConfig()
	if a.Root != "/" || a.Root != cfg.Exec.Root || a.MarkDir != cfg.Exec.MarkDir || a.Jobs != cfg.Store {
		t.Errorf("API root %q, markers %q, jobs %+v; the runner has %q, %q, %+v", a.Root, a.MarkDir, a.Jobs,
			cfg.Exec.Root, cfg.Exec.MarkDir, cfg.Store)
	}
	if !reflect.DeepEqual(a.Runner, run.Exec{Env: openrc.Env}) {
		t.Errorf("runner %+v", a.Runner)
	}
	if want := (jobs.ExecLauncher{Binary: "/proc/self/exe", Env: openrc.Env}); !reflect.DeepEqual(a.Launcher, want) {
		t.Errorf("launcher %+v; want %+v", a.Launcher, want)
	}
	// the tools are looked up in the bin directories of the host (a lookup reads nothing else)
	if got, want := a.Tools(), openrc.FindTools("/"); got != want {
		t.Errorf("tools %+v; want %+v", got, want)
	}
	if a.Now != nil {
		t.Error("the API of the host has its own clock")
	}
}

// register mounts the API. Nothing of the host is read: a change without admin mode is refused before it looks at
// anything, and an id that is no job id is no job.
func TestRegister(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	register(app)
	routes := map[string]bool{}
	for _, r := range app.GetRoutes() {
		routes[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{"GET /view", "GET /services/:name", "GET /services/:name/logs",
		"GET /services/:name/logs/stream", "POST /jobs", "GET /jobs/:id"} {
		if !routes[want] {
			t.Errorf("no route %s; routes %v", want, routes)
		}
	}
	user := sdk.User{Name: "myUser", UID: 1001, Lang: "en"}
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/jobs", strings.NewReader("{}"), user))
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /jobs without admin mode: status %d; want 403", resp.StatusCode)
	}
	resp = sdktest.Do(t, app, sdktest.Request("GET", "/jobs/x", nil, user))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /jobs/x: status %d; want 404", resp.StatusCode)
	}
}

// sdk.New panics on an invalid manifest, so building the extension proves the embedded manifest.json is valid. The
// committed manifest holds the placeholder version: a release gets its version from the git tag (manifestWithVersion).
func TestManifest(t *testing.T) {
	m := sdk.New(sdk.Options{Manifest: manifest}).Manifest
	if m.ID != "openrc" {
		t.Fatalf("manifest id %q, want openrc", m.ID)
	}
	if m.Version != "0.0.0-dev" {
		t.Fatalf("manifest version %q, want the placeholder 0.0.0-dev", m.Version)
	}
	if m.Menu.Section != "system" || m.Menu.Order != 40 {
		t.Fatalf("menu %+v, want section system, order 40", m.Menu)
	}
	if m.Name["de"] != "Dienste" || m.Name["en"] != "Services" {
		t.Fatalf("name %v, want Dienste/Services", m.Name)
	}
	if m.RequiresAdmin {
		t.Fatal("requiresAdmin must be false: everybody may see the services")
	}
}

// Without a release version the manifest stays as it is.
func TestManifestWithoutVersion(t *testing.T) {
	got, err := manifestWithVersion(manifest, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, manifest) {
		t.Fatalf("manifestWithVersion(manifest, \"\") = %q, want the manifest unchanged", got)
	}
}

// A release version replaces the version field and nothing else.
func TestManifestWithVersion(t *testing.T) {
	const v = "0.2.0-rc.1"
	raw, err := manifestWithVersion(manifest, v)
	if err != nil {
		t.Fatal(err)
	}
	got := sdk.New(sdk.Options{Manifest: raw}).Manifest
	want := sdk.New(sdk.Options{Manifest: manifest}).Manifest
	want.Version = v
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %+v, want the manifest.json one with version %s: %+v", got, v, want)
	}
	var gotFields, wantFields map[string]any
	if err := json.Unmarshal(raw, &gotFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &wantFields); err != nil {
		t.Fatal(err)
	}
	wantFields["version"] = v
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("manifest fields = %v, want %v", gotFields, wantFields)
	}
}

func TestManifestWithVersionInvalidJSON(t *testing.T) {
	for _, raw := range []string{"{", "[]", "null"} {
		if got, err := manifestWithVersion([]byte(raw), "0.2.0"); err == nil {
			t.Fatalf("manifestWithVersion(%q) returned %q and no error", raw, got)
		}
	}
}
