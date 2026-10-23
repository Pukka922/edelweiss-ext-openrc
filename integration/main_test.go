//go:build integration

// Package integration runs the commands of the extension against the real OpenRC of Alpine: rc-service, rc-update and
// rc-status, with the test services of testdata/init.d, first as captured outputs, then through the packages of the
// extension (reading the services and their logs, the plans of the changes and the executor of the jobs). The commands
// run through run.Exec, the runner of the extension, so OpenRC gets exactly the arguments, the environment and the
// working directory the extension gives it. scripts/test-alpine.sh runs each test in a throwaway Alpine container of
// its own, which installs openrc, installs the test services, creates /run/openrc/softlevel and starts busybox syslogd;
// the tests run as root there (EDELWEISS_ALPINE_TEST=1), TestReadAsUser as nobody (EDELWEISS_ALPINE_TEST=user).
// Anywhere else the tests refuse to run.
package integration

import (
	"fmt"
	"os"
	"testing"
)

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestMain(m *testing.M) {
	pass, root := os.Getenv("EDELWEISS_ALPINE_TEST"), os.Geteuid() == 0
	if !exists("/.dockerenv") || !exists("/etc/alpine-release") || !(pass == "1" && root || pass == "user" && !root) {
		fmt.Println("integration: skipped – these tests start and stop services with OpenRC; scripts/test-alpine.sh runs them in throwaway Alpine containers")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
