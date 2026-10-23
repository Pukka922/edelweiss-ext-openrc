package openrc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// cacheNoise is what rc-service prints before its first answer when an init script is newer than OpenRC's dependency
// cache (on stderr, so in the output run.Exec gives).
const cacheNoise = "Service 'hwdrivers' needs non existent service 'dev'\n" +
	"Service 'hwdrivers' needs non existent service 'dev'\n" +
	" * Caching service dependencies ... [ ok ]\n"

func TestDetails(t *testing.T) {
	tools := Tools{RcService: "/sbin/rc-service"}
	fake := &run.Fake{Results: map[string]run.FakeResult{
		"/sbin/rc-service t-needs ineed":    {Output: cacheNoise + readTestdata(t, "ineed.txt")},
		"/sbin/rc-service t-needs iuse":     {Output: "\n"},
		"/sbin/rc-service t-needs iafter":   {Output: "\n"},
		"/sbin/rc-service t-needs needsme":  {Output: "\n"},
		"/sbin/rc-service t-daemon ineed":   {Output: "\n"},
		"/sbin/rc-service t-daemon iuse":    {Output: "t-super \n"},
		"/sbin/rc-service t-daemon iafter":  {Output: "t-oneshot t-crash \n"},
		"/sbin/rc-service t-daemon needsme": {Output: readTestdata(t, "needsme.txt")},
		"/sbin/rc-service t-nosuch ineed":   {Output: " * rc-service: service `t-nosuch' does not exist\n", Status: 1},
	}}
	r := Reader{Runner: fake, Tools: tools}
	d, err := r.Details(ctx, "t-needs")
	if want := (Details{Needs: []string{"t-daemon"}, Uses: []string{}, After: []string{}, NeededBy: []string{}}); err != nil ||
		!reflect.DeepEqual(d, want) {
		t.Errorf("Details(t-needs) = %+v, %v, want %+v", d, err, want)
	}
	want := []string{"/sbin/rc-service t-needs ineed", "/sbin/rc-service t-needs iuse", "/sbin/rc-service t-needs iafter",
		"/sbin/rc-service t-needs needsme"}
	if got := fake.Commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands\n got %q\nwant %q", got, want)
	}
	for _, call := range fake.Calls {
		if call.Cmd.Timeout != 30*time.Second {
			t.Errorf("%s: timeout %v, want 30s", call.Cmd, call.Cmd.Timeout)
		}
	}

	d, err = r.Details(ctx, "t-daemon")
	if want := (Details{Needs: []string{}, Uses: []string{"t-super"}, After: []string{"t-crash", "t-oneshot"},
		NeededBy: []string{"t-needs"}}); err != nil || !reflect.DeepEqual(d, want) {
		t.Errorf("Details(t-daemon) = %+v, %v, want %+v", d, err, want)
	}
	if b, _ := json.Marshal(d); string(b) != `{"needs":[],"uses":["t-super"],"after":["t-crash","t-oneshot"],"neededBy":["t-needs"]}` {
		t.Errorf("JSON %s", b)
	}

	if d, err := r.Details(ctx, "t-nosuch"); err == nil || !reflect.DeepEqual(d, Details{}) {
		t.Errorf("a failing command: %+v, %v, want an error", d, err)
	}
	n := len(fake.Commands())
	if _, err := r.Details(ctx, "../x"); err == nil || len(fake.Commands()) != n {
		t.Errorf("an invalid name: %v, %d commands, want an error and none", err, len(fake.Commands())-n)
	}
}

func TestDependentsTransitive(t *testing.T) {
	answers := map[string]string{ // the direct dependents of each service, as rc-service <s> needsme prints them
		"a": "b \n", "b": "c \n", "c": "\n",
		"x": "y \n", "y": "x \n", // a cycle
		"d": "e f \n", "e": "g \n", "f": "g \n", "g": "\n", // g is reached twice
		"self": "self other \n", "other": "\n",
		"noisy": cacheNoise + "a \n",
		"p":     "broken \n",
		// OpenRC's own answer is transitive already (t-xp-chain needs t-needs, which needs t-daemon), and not filtered
		// by state (t-needs2 is stopped).
		"t-daemon": "t-xp-chain t-needs t-needs2 t-xp-deps \n", "t-needs": "t-xp-chain \n", "t-needs2": "\n",
		"t-xp-chain": "\n", "t-xp-deps": "\n",
	}
	fake := &run.Fake{
		Results: map[string]run.FakeResult{"/sbin/rc-service broken needsme": {Output: "oops\n", Status: 1}},
		OnRun: func(c run.Cmd) (string, error) {
			if len(c.Args) == 2 && c.Args[1] == "needsme" {
				if out, ok := answers[c.Args[0]]; ok {
					return out, nil
				}
			}
			return "", fmt.Errorf("unexpected command %s", c)
		},
	}
	r := Reader{Runner: fake, Tools: Tools{RcService: "/sbin/rc-service"}}
	for _, tc := range []struct {
		name string
		want []string
		runs int // needsme runs: one per service reached
	}{
		{"a", []string{"b", "c"}, 3},
		{"x", []string{"y"}, 2},
		{"d", []string{"e", "f", "g"}, 4},
		{"c", []string{}, 1},
		{"self", []string{"other"}, 2},
		{"noisy", []string{"a", "b", "c"}, 4},
		{"t-daemon", []string{"t-needs", "t-needs2", "t-xp-chain", "t-xp-deps"}, 5},
	} {
		before := len(fake.Commands())
		got, err := r.Dependents(ctx, tc.name)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Dependents(%s) = %q, %v, want %q", tc.name, got, err, tc.want)
		}
		if runs := len(fake.Commands()) - before; runs != tc.runs {
			t.Errorf("Dependents(%s): %d runs of needsme, want %d", tc.name, runs, tc.runs)
		}
	}
	if got, err := r.Dependents(ctx, "p"); err == nil || got != nil {
		t.Errorf("a failing needsme: %q, %v, want an error", got, err)
	}
	if got, err := r.Dependents(ctx, "a b"); err == nil || got != nil {
		t.Errorf("an invalid name: %q, %v, want an error", got, err)
	}
}
