package main

import (
	"net/http"
	"testing"
	"time"
)

type warnSpy struct{ msgs []string }

func (w *warnSpy) Warn(msg string, _ ...any) { w.msgs = append(w.msgs, msg) }

// S-14: the listener has a write bound by default; the env can widen it or
// (loudly) turn it off, and garbage keeps the default.
func TestHTTPServerWriteTimeout(t *testing.T) {
	for _, c := range []struct {
		env   string
		want  time.Duration
		warns int
	}{
		{"", DefaultWriteTimeout, 0},
		{"90s", 90 * time.Second, 0},
		{"0", 0, 1},
		{"soon", DefaultWriteTimeout, 1},
		{"-5s", DefaultWriteTimeout, 1},
	} {
		t.Run("COLAB_HTTP_WRITE_TIMEOUT="+c.env, func(t *testing.T) {
			t.Setenv("COLAB_HTTP_WRITE_TIMEOUT", c.env)
			spy := &warnSpy{}
			srv := httpServer(":0", http.NotFoundHandler(), spy)
			if srv.WriteTimeout != c.want {
				t.Fatalf("WriteTimeout = %s, want %s", srv.WriteTimeout, c.want)
			}
			if srv.ReadHeaderTimeout != 5*time.Second {
				t.Fatalf("ReadHeaderTimeout = %s, want 5s (unchanged)", srv.ReadHeaderTimeout)
			}
			if len(spy.msgs) != c.warns {
				t.Fatalf("warnings = %v, want %d", spy.msgs, c.warns)
			}
		})
	}
}

// S-14: the pool size is exposed; nonsense keeps pgx's default and says so.
func TestPoolOptions(t *testing.T) {
	for _, c := range []struct {
		max, min string
		want     [2]int32
		warns    int
	}{
		{"", "", [2]int32{0, 0}, 0},
		{"32", "4", [2]int32{32, 4}, 0},
		{"many", "", [2]int32{0, 0}, 1},
		{"0", "", [2]int32{0, 0}, 1},
		{"8", "16", [2]int32{8, 8}, 1}, // min lowered to max
	} {
		t.Run("max="+c.max+" min="+c.min, func(t *testing.T) {
			t.Setenv("COLAB_DB_MAX_CONNS", c.max)
			t.Setenv("COLAB_DB_MIN_CONNS", c.min)
			spy := &warnSpy{}
			o := poolOptions(spy)
			if o.MaxConns != c.want[0] || o.MinConns != c.want[1] {
				t.Fatalf("options = %+v, want max %d min %d", o, c.want[0], c.want[1])
			}
			if len(spy.msgs) != c.warns {
				t.Fatalf("warnings = %v, want %d", spy.msgs, c.warns)
			}
		})
	}
}
