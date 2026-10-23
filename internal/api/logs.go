package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/logs"
)

// maxLines is how many of the last lines of a log an answer holds.
const maxLines = 500

// LogsResponse is the answer of GET /services/:name/logs: the sources of the service, and the last lines of the one
// shown (Source), as logs.Lines has them: Offset is where to follow it from. Source is "" when none was asked for and
// none can be read; a source asked for that cannot be read has no lines.
type LogsResponse struct {
	Sources []logs.Source `json:"sources"`
	Source  string        `json:"source"`
	Lines   []string      `json:"lines"`
	Offset  int64         `json:"offset"`
	Capped  bool          `json:"capped"`
}

// logLines answers GET /services/:name/logs?source=<id>, for everybody: the last 500 lines of the source, by default
// of the first that can be read (see logSource).
func (a *API) logLines(c fiber.Ctx) error {
	l, err := a.logSource(c)
	if err != nil {
		return err
	}
	resp := LogsResponse{Sources: l.sources, Lines: []string{}}
	if !l.picked {
		return c.JSON(resp)
	}
	resp.Source = l.source.ID
	if l.source.Readable {
		tail, err := logs.Tail(a.Root, l.source, l.programs, maxLines)
		if err != nil {
			return toHTTP(fmt.Errorf("reading %s: %w", l.source.Path, err))
		}
		resp.Lines, resp.Offset, resp.Capped = tail.Lines, tail.Offset, tail.Capped
	}
	return c.JSON(resp)
}

// serviceLog is the log of a service that a request asks for.
type serviceLog struct {
	service  string
	sources  []logs.Source // all the logs of the service
	source   logs.Source   // the one asked for (see pick)
	picked   bool          // false when there is none to pick
	programs []string      // the names under which the system log has the lines of the service
}

// logSource is what both requests for the log of a service look up: the name in the path (see serviceName), the
// service in a fresh list (see readService), its sources, and the one the query asks for (see pick).
func (a *API) logSource(c fiber.Ctx) (serviceLog, error) {
	name, err := serviceName(c)
	if err != nil {
		return serviceLog{}, err
	}
	r := a.reader()
	s, err := readService(ctxOf(c), r, name)
	if err != nil {
		return serviceLog{}, err
	}
	l := serviceLog{service: name, programs: r.Programs(name)}
	l.sources = logs.Sources(a.Root, s, l.programs)
	if l.source, l.picked, err = pick(l.sources, name, c.Query("source")); err != nil {
		return serviceLog{}, err
	}
	return l, nil
}

// pick is the source of the service with the id, or without an id the first that can be read; ok is false when there
// is none. An id that is not among the sources is not_found.
func pick(sources []logs.Source, service, id string) (src logs.Source, ok bool, err error) {
	if id == "" {
		i := slices.IndexFunc(sources, func(s logs.Source) bool { return s.Readable })
		if i < 0 {
			return logs.Source{}, false, nil
		}
		return sources[i], true, nil
	}
	i := slices.IndexFunc(sources, func(s logs.Source) bool { return s.ID == id })
	if i < 0 {
		return logs.Source{}, false, notFound("%s has no log %s", service, id)
	}
	return sources[i], true, nil
}

// streamKey is the key of c.Locals under which prepareStream hands the stream what it follows.
const streamKey = "openrc.stream"

// following is what a stream follows: the source, the programs whose lines of the system log are the service's (see
// logs.Follow), and the byte offset to start at.
type following struct {
	source   logs.Source
	programs []string
	offset   int64
}

// prepareStream checks the request of GET /services/:name/logs/stream?source=<id>&offset=<n> before the stream starts,
// so that a refusal is a proper error answer: the offset (the one of the answer of the lines), then what logSource
// looks up. The source has to be one that can be read: a stream of another would never send a line. What the stream
// needs goes into c.Locals: the stream runs after this handler has returned (see logStream), and its values come from
// the host, not from the buffers of the request.
func (a *API) prepareStream(c fiber.Ctx) error {
	offset, err := strconv.ParseInt(c.Query("offset"), 10, 64)
	if err != nil || offset < 0 {
		return invalid("offset", "the offset must be the byte offset of the lines of the log")
	}
	l, err := a.logSource(c)
	if err != nil {
		return err
	}
	if !l.picked || !l.source.Readable {
		return notFound("%s has no log to follow that can be read", l.service)
	}
	c.Locals(streamKey, following{source: l.source, programs: l.programs, offset: offset})
	return c.Next()
}

// heartbeat is how often a log stream sends a comment while there is no line. Only a write that fails tells that the
// client has left (edelweissd, too, notices it only when it relays a write), so a stream of a quiet log ends a few
// heartbeats after the client left: with the 15 s of sdk.SSE that was a minute and more.
const heartbeat = 2 * time.Second

// logStream is the handler of the stream of a log: a.stream in the SSE middleware of Fiber, as sdk.SSE has it, but with
// the shorter heartbeat (sdk.SSE takes no interval). As with sdk.SSE, the stream runs while the response is written,
// after the route's handlers have returned, and may still run when the request has been recycled: it reads c at its
// start only.
func logStream(a *API) fiber.Handler {
	return sse.New(sse.Config{Handler: a.stream, HeartbeatInterval: heartbeat})
}

// stream follows the log that prepareStream chose, every second, until the client leaves (a write that fails tells: a
// line, or the heartbeat). Each new line is a line event, its data the line as a JSON string; a file that shrank or was
// replaced is a reset event (data {}), and its lines from the start follow. The comment it opens with sends the
// headers, so that the client sees the stream open before the first line.
func (a *API) stream(c fiber.Ctx, s *sse.Stream) error {
	fl := c.Locals(streamKey).(following) // the only use of c, at the start (see logStream)
	if err := s.Comment("following"); err != nil {
		return nil
	}
	logs.Follow(s.Context(), a.Root, fl.source, fl.programs, fl.offset, a.followEvery(), func(e logs.Event) bool {
		return s.Event(toEvent(e)) == nil
	})
	return nil
}

// toEvent is the event of the stream for what Follow saw.
func toEvent(e logs.Event) sse.Event {
	if e.Kind != "line" {
		return sse.Event{Name: e.Kind, Data: "{}"}
	}
	data, _ := json.Marshal(e.Line) // a string always encodes
	return sse.Event{Name: "line", Data: json.RawMessage(data)}
}
