package config

import (
	"path/filepath"
	"testing"
)

// D-24: the level is configurable, and $COLAB_DAEMON_LOG wins — the daemon
// whose log you need is usually one already running under a supervisor.
func TestLogLevelEffective(t *testing.T) {
	for _, tc := range []struct {
		name, field, env, want string
	}{
		{"absent", "", "", ""},
		{"config only", "debug", "", "debug"},
		{"env only", "", "debug", "debug"},
		{"env beats config", "info", "debug", "debug"},
		{"env beats config the other way", "debug", "info", "info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("COLAB_DAEMON_LOG", tc.env)
			if got := (Config{LogLevel: tc.field}).LogLevelEffective(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// The field survives a save/load round trip, and a daemon.json written before
// it existed still loads (empty → the default level).
func TestLogLevelRoundTrip(t *testing.T) {
	t.Setenv("COLAB_DAEMON_LOG", "")
	p := filepath.Join(t.TempDir(), "daemon.json")
	if err := Save(p, Config{ServerURL: "https://s", RuntimeID: "rt", DaemonToken: "cdt", LogLevel: "debug"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.LogLevelEffective() != "debug" {
		t.Fatalf("log_level %q", got.LogLevelEffective())
	}
}
