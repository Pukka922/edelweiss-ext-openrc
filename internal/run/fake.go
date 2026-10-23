package run

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Fake is a Runner for tests. It records every call. A call whose Cmd.String() is a key of Results gets that output
// and status (as with Exec, a status other than 0 and not in Cmd.OK is an *Error); any other call is answered by OnRun
// when it is set (without the lock, so OnRun may look at the Fake), and else succeeds without output.
type Fake struct {
	mu      sync.Mutex
	Calls   []FakeCall
	Results map[string]FakeResult // key: Cmd.String()
	OnRun   func(c Cmd) (string, error)
}

// FakeCall is one call of a Fake.
type FakeCall struct {
	Cmd Cmd
}

// FakeResult is the answer of a Fake to a command. Status -1 stands for a command that did not start.
type FakeResult struct {
	Output string
	Status int
}

// Run records the call and answers it.
func (f *Fake) Run(_ context.Context, c Cmd) (string, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, FakeCall{Cmd: c})
	r, ok := f.Results[c.String()]
	onRun := f.OnRun
	f.mu.Unlock()
	if ok {
		err := fmt.Errorf("exit status %d", r.Status)
		if r.Status < 0 {
			err = errors.New("did not start")
		}
		return r.Output, failure(c, r.Status, r.Output, err)
	}
	if onRun != nil {
		return onRun(c)
	}
	return "", nil
}

// Commands lists the recorded calls, each as its Cmd.String().
func (f *Fake) Commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, call := range f.Calls {
		out = append(out, call.Cmd.String())
	}
	return out
}
