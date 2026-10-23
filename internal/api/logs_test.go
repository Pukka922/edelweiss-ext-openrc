package api

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/logs"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// nginxSyslog are the lines of nginx in the system log of the test host.
var nginxSyslog = []string{
	"Oct 20 07:50:00 host daemon.info nginx[4242]: nginx started",
	"Oct 20 07:52:00 host daemon.notice nginx[4242]: reloading",
}

// lock makes p unreadable (mode 0000) until the test ends.
func (f *harness) lock(p string) {
	f.t.Helper()
	if os.Geteuid() == 0 {
		f.t.Skip("root reads a file of mode 0000")
	}
	if err := os.Chmod(f.path(p), 0); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = os.Chmod(f.path(p), 0o644) })
}

// logsOf asks for the logs of the service; query is like "?source=syslog".
func (f *harness) logsOf(name, query string) LogsResponse {
	f.t.Helper()
	return decode[LogsResponse](f.t, f.send("GET", "/services/"+name+"/logs"+query, nil, plain).want(f.t, http.StatusOK))
}

// size is the size of the file p of the test host.
func (f *harness) size(p string) int64 {
	f.t.Helper()
	st, err := os.Stat(f.path(p))
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Size()
}

func (f *harness) appendTo(p, text string) {
	f.t.Helper()
	file, err := os.OpenFile(f.path(p), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		f.t.Fatal(err)
	}
}

// The last 500 lines of the first source that can be read, or of the source asked for; the sources come with them.
func TestLogsTail(t *testing.T) {
	f := newHarness(t)
	l := f.logsOf("nginx", "")
	want := []logs.Source{
		{ID: "file:nginx.log", Label: "/var/log/nginx.log", Path: "/var/log/nginx.log", Readable: true},
		{ID: "syslog", Label: "/var/log/messages (nur nginx)", Path: "/var/log/messages", Readable: true},
	}
	if !slices.Equal(l.Sources, want) {
		t.Fatalf("sources %+v; want %+v", l.Sources, want)
	}
	if l.Source != "file:nginx.log" || len(l.Lines) != 500 || l.Lines[0] != "GET /page/101 200" ||
		l.Lines[499] != "GET /page/600 200" || l.Offset != f.size("/var/log/nginx.log") || l.Capped {
		t.Errorf("source %q, %d lines (%q … %q), offset %d, capped %v", l.Source, len(l.Lines), l.Lines[0],
			l.Lines[len(l.Lines)-1], l.Offset, l.Capped)
	}

	l = f.logsOf("nginx", "?source=syslog")
	if l.Source != "syslog" || !slices.Equal(l.Lines, nginxSyslog) || l.Offset != f.size("/var/log/messages") {
		t.Errorf("syslog: source %q, lines %q, offset %d", l.Source, l.Lines, l.Offset)
	}
	// a service with a source but no lines in it
	a := f.send("GET", "/services/docker/logs", nil, plain).want(t, http.StatusOK)
	if !strings.Contains(a.raw, `"source":"syslog","lines":[]`) {
		t.Errorf("docker: %s; want the system log without lines", a.raw)
	}
	e := f.send("GET", "/services/nginx/logs?source=file:other.log", nil, plain).wantError(t, http.StatusNotFound,
		"not_found")
	if !strings.Contains(e["message"].(string), "file:other.log") {
		t.Errorf("message %q", e["message"])
	}
}

// A source the process may not read is listed as not readable, and is not the one shown by default. Asked for, it has
// no lines; without a source that can be read, there is none to show.
func TestLogsNotReadable(t *testing.T) {
	f := newHarness(t)
	f.lock("/var/log/nginx.log")
	l := f.logsOf("nginx", "")
	if len(l.Sources) != 2 || l.Sources[0].ID != "file:nginx.log" || l.Sources[0].Readable || !l.Sources[1].Readable {
		t.Fatalf("sources %+v; want the log file listed as not readable", l.Sources)
	}
	if l.Source != "syslog" || !slices.Equal(l.Lines, nginxSyslog) {
		t.Errorf("source %q, lines %q; want the system log, the first that can be read", l.Source, l.Lines)
	}
	l = f.logsOf("nginx", "?source=file:nginx.log")
	if l.Source != "file:nginx.log" || len(l.Lines) != 0 || l.Lines == nil || l.Offset != 0 {
		t.Errorf("source %q, lines %q, offset %d; want no lines", l.Source, l.Lines, l.Offset)
	}
	// a stream of it would never send a line (while the system log can be read)
	f.send("GET", "/services/nginx/logs/stream?offset=0&source=file:nginx.log", nil, plain).wantError(t,
		http.StatusNotFound, "not_found")
	f.lock("/var/log/messages")
	a := f.send("GET", "/services/nginx/logs", nil, plain).want(t, http.StatusOK)
	if l := decode[LogsResponse](t, a); l.Source != "" || len(l.Sources) != 2 || l.Sources[1].Readable ||
		!strings.Contains(a.raw, `"lines":[]`) {
		t.Errorf("answer %s; want both listed, none shown", a.raw)
	}
	f.send("GET", "/services/nginx/logs/stream?offset=0", nil, plain).wantError(t, http.StatusNotFound, "not_found")
}

// event is an event of a stream; a comment (the heartbeat) is one named ":".
type event struct{ name, data string }

// comment is the name of the event of a comment line.
const comment = ":"

// listen serves the API on a listener of the loopback: a stream does not end by itself, so app.Test (which reads a
// response to its end) cannot be used.
func (f *harness) listen() string {
	f.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		f.t.Fatal(err)
	}
	go func() { _ = f.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	f.t.Cleanup(func() { _ = f.app.ShutdownWithTimeout(5 * time.Second) })
	return ln.Addr().String()
}

// stream opens the stream at path of the server at addr and returns its events and the func that leaves it.
func (f *harness) stream(addr, path string) (<-chan event, func()) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header = sdktest.Request("GET", path, nil, plain).Header
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		f.t.Fatalf("the stream did not open: %v", err)
	}
	if took := time.Since(start); took > time.Second { // the first heartbeat comes after 2 s
		cancel()
		f.t.Fatalf("the stream opened after %v; want it open before the first event", took)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		f.t.Fatalf("status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body := bufio.NewReader(resp.Body)
	if first, err := body.ReadString('\n'); err != nil || !strings.HasPrefix(first, ":") {
		cancel()
		f.t.Fatalf("first line %q (%v); want the comment that opens the stream", first, err)
	}
	events := make(chan event, 100)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(body)
		var ev event
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, comment):
				events <- event{name: comment, data: strings.TrimPrefix(line, comment)}
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.name != "":
				events <- ev
				ev = event{}
			}
		}
	}()
	return events, func() { cancel(); resp.Body.Close() }
}

// next is the next event of the stream that is no comment, within 5 s.
func next(t *testing.T, events <-chan event) event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the stream ended")
			}
			if ev.name != comment {
				return ev
			}
		case <-timeout:
			t.Fatal("no event within 5 s")
		}
	}
}

// An idle stream sends a heartbeat every 2 s, so that it notices soon when the client has left: only a write that
// fails tells (a heartbeat or a line; edelweissd notices a client that left only on a write as well). The default of
// 15 s let a closed stream of a quiet log linger for a minute and more.
func TestLogsStreamHeartbeat(t *testing.T) {
	f := newHarness(t)
	first := f.logsOf("nginx", "")
	addr := f.listen()
	q := url.Values{"source": {"file:nginx.log"}, "offset": {strconv.FormatInt(first.Offset, 10)}}
	events, leave := f.stream(addr, "/services/nginx/logs/stream?"+q.Encode())
	defer leave()
	start := time.Now()
	for beats := 0; beats < 2; {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the stream ended")
			}
			if ev != (event{comment, ""}) {
				t.Fatalf("event %+v; want only heartbeats from an idle log", ev)
			}
			beats++
		case <-time.After(3 * time.Second):
			t.Fatalf("no heartbeat within 3 s (%d after %v)", beats, time.Since(start))
		}
	}
	if d := time.Since(start); d < 3*time.Second || d > 5*time.Second {
		t.Errorf("two heartbeats after %v; want one every 2 s", d)
	}
	// the client leaves: nothing is written to the log, and the stream ends all the same, within a few heartbeats
	leave()
	if err := f.app.ShutdownWithTimeout(10 * time.Second); err != nil {
		t.Errorf("the idle stream did not end when the client left: %v", err)
	}
}

// The stream follows the source from the offset of the first answer: a line added to the file comes as a line event
// (its data the line as a JSON string), a file that shrank as a reset and its lines from the start. The stream ends
// when the client leaves.
func TestLogsStream(t *testing.T) {
	f := newHarness(t)
	f.api.every = 20 * time.Millisecond
	first := f.logsOf("nginx", "")
	addr := f.listen()
	q := url.Values{"source": {"file:nginx.log"}, "offset": {strconv.FormatInt(first.Offset, 10)}}
	events, leave := f.stream(addr, "/services/nginx/logs/stream?"+q.Encode())
	defer leave()
	f.appendTo("/var/log/nginx.log", "GET /new 200\n")
	if ev := next(t, events); ev != (event{"line", `"GET /new 200"`}) {
		t.Fatalf("event %+v; want the new line", ev)
	}
	f.appendTo("/var/log/nginx.log", `GET /q?a="b" 200`+"\n")
	if ev := next(t, events); ev != (event{"line", `"GET /q?a=\"b\" 200"`}) {
		t.Fatalf("event %+v; want the line as a JSON string", ev)
	}
	f.write("/var/log/nginx.log", "fresh\n")
	if ev := next(t, events); ev != (event{"reset", "{}"}) {
		t.Fatalf("event %+v; want a reset", ev)
	}
	if ev := next(t, events); ev != (event{"line", `"fresh"`}) {
		t.Fatalf("event %+v; want the line of the new start", ev)
	}

	// the client leaves: the next lines fail to go out, and the stream ends, so the server shuts down at once (the
	// first write to a closed connection may still succeed, hence a line every 20 ms)
	leave()
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				if file, err := os.OpenFile(f.path("/var/log/nginx.log"), os.O_WRONLY|os.O_APPEND, 0); err == nil {
					fmt.Fprintf(file, "after %d\n", i)
					file.Close()
				}
			}
		}
	}()
	err := f.app.ShutdownWithTimeout(5 * time.Second)
	close(stop)
	<-stopped
	if err != nil {
		t.Errorf("the stream did not end when the client left: %v", err)
	}
}

// The stream needs a source that is there and an offset.
func TestLogsStreamRefusals(t *testing.T) {
	f := newHarness(t)
	for _, offset := range []string{"", "?offset=", "?offset=-1", "?offset=x", "?offset=1.5", "?source=syslog"} {
		e := f.send("GET", "/services/nginx/logs/stream"+offset, nil, plain).wantError(t, http.StatusBadRequest,
			"invalid_input")
		if e["field"] != "offset" {
			t.Errorf("%q: error %v; want the field offset", offset, e)
		}
	}
	f.send("GET", "/services/nginx/logs/stream?offset=0&source=file:x.log", nil, plain).wantError(t, http.StatusNotFound,
		"not_found")
}
