package openrc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Details are the relations of a service as rc-service prints them, each list sorted.
type Details struct {
	Needs    []string `json:"needs"`    // ineed
	Uses     []string `json:"uses"`     // iuse
	After    []string `json:"after"`    // iafter
	NeededBy []string `json:"neededBy"` // needsme
}

// Details runs rc-service <name> ineed, iuse, iafter and needsme, one after the other. OpenRC lists in iuse and iafter
// only the services of the current runlevel; ineed and needsme take every runlevel and state.
func (r Reader) Details(ctx context.Context, name string) (Details, error) {
	var d Details
	for _, q := range []struct {
		what string
		into *[]string
	}{{"ineed", &d.Needs}, {"iuse", &d.Uses}, {"iafter", &d.After}, {"needsme", &d.NeededBy}} {
		names, err := r.relation(ctx, name, q.what)
		if err != nil {
			return Details{}, err
		}
		*q.into = names
	}
	return d, nil
}

// Dependents are the services that need the service, directly or through others: needsme, asked again for every name
// it gives until no new name comes (a cycle ends there). Stopped services are among them. Sorted, each once, never
// the service itself.
func (r Reader) Dependents(ctx context.Context, name string) ([]string, error) {
	seen := map[string]bool{name: true}
	found := []string{}
	for queue := []string{name}; len(queue) > 0; queue = queue[1:] {
		names, err := r.relation(ctx, queue[0], "needsme")
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				found = append(found, n)
				queue = append(queue, n)
			}
		}
	}
	slices.Sort(found)
	return found, nil
}

// relation runs rc-service <name> <what> and returns the names it printed.
func (r Reader) relation(ctx context.Context, name, what string) ([]string, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid service name %q", name)
	}
	if r.Tools.RcService == "" {
		return nil, errors.New("rc-service is not installed")
	}
	out, err := r.Runner.Run(ctx, r.Tools.query(name, what))
	if err != nil {
		return nil, err
	}
	return serviceNames(out), nil
}

// serviceNames are the names in the output of a relation: rc-service prints them on one line, each followed by a
// space. A line that holds anything but names is skipped: before its first answer after an init script changed,
// OpenRC notes that it rebuilds its dependency cache (" * Caching service dependencies ... [ ok ]", "Service 'x' needs
// non existent service 'y'"). Sorted, each once.
func serviceNames(out string) []string {
	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if !slices.ContainsFunc(fields, func(f string) bool { return !ValidName(f) }) {
			names = append(names, fields...)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}
