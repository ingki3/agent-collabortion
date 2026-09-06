package client

// The review found DoStream "sharing" c.http.Transport when that field was
// nil, so the download ran on the default transport with no dial, TLS or
// response-header bound at all. A comment claiming a bound is not a bound;
// these assert the wiring directly, from inside the package.

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func TestNewWiresARealBoundedTransport(t *testing.T) {
	c := New(Config{ServerURL: "http://example.invalid", Token: "ctk_x", Timeout: 7 * time.Second})
	if c.transport == nil {
		t.Fatal("Client.transport is nil — DoStream would run unbounded")
	}
	if c.http.Transport == nil {
		t.Fatal("the buffered client's Transport is nil; DoStream shares this field")
	}
	if c.http.Transport != c.transport {
		t.Fatal("Do and DoStream must share one transport")
	}
	tr, ok := c.transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.transport)
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want the configured 7s — without it a server that "+
			"accepts the request and never answers hangs the download for ever", tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != 7*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want 7s", tr.TLSHandshakeTimeout)
	}
	// The shared default transport must not have been mutated.
	if def, ok := http.DefaultTransport.(*http.Transport); ok && def.ResponseHeaderTimeout != 0 {
		t.Fatal("http.DefaultTransport was mutated; it must be cloned")
	}
}

// An explicitly supplied client keeps its own transport — the CLI never does
// this, but the escape hatch must not be silently overridden.
func TestExplicitHTTPClientKeepsItsTransport(t *testing.T) {
	own := &http.Transport{}
	c := New(Config{ServerURL: "http://example.invalid", Token: "ctk_x", HTTP: &http.Client{Transport: own}})
	if c.transport != own {
		t.Fatalf("transport = %v, want the caller's", c.transport)
	}
}

// The idle bound is on silence, not on total duration: a reader that keeps
// delivering must never be cut off, however long it runs.
func TestIdleReaderPassesThroughWhenProgressing(t *testing.T) {
	src := &pacedReader{chunks: 8, gap: 30 * time.Millisecond}
	r := newIdleReader(src, 200*time.Millisecond)
	defer r.Close()
	buf := make([]byte, 1)
	total := 0
	for {
		n, err := r.Read(buf)
		total += n
		if err != nil {
			if total != 8 {
				t.Fatalf("read %d bytes then %v, want 8 clean bytes over %v", total, err, 240*time.Millisecond)
			}
			return
		}
	}
}

// pacedReader hands back one byte at a time with a pause between them.
type pacedReader struct {
	chunks int
	gap    time.Duration
	sent   int
}

func (p *pacedReader) Read(b []byte) (int, error) {
	if p.sent >= p.chunks {
		return 0, io.EOF
	}
	time.Sleep(p.gap)
	p.sent++
	b[0] = 'x'
	return 1, nil
}

func (p *pacedReader) Close() error { return nil }
