// Package config is ~/.colab/daemon.json.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config is the persisted daemon state (pairing result + local settings).
type Config struct {
	ServerURL   string `json:"server_url"`
	RuntimeID   string `json:"runtime_id,omitempty"`
	DaemonToken string `json:"daemon_token,omitempty"`
	WorkdirRoot string `json:"workdir_root,omitempty"`
	Capacity    int    `json:"capacity,omitempty"`
	// StderrDir keeps per-attempt runtime stderr logs. Empty → <workdir_root>/.colab/logs.
	StderrDir string `json:"stderr_dir,omitempty"`
	// ColabBin is the colab CLI registered as the attempt's MCP server
	// (`colab mcp serve`, harness §2 / colab-cli.md §3). Empty → the `colab`
	// next to the daemon executable if present, else `colab` on PATH.
	ColabBin string `json:"colab_bin,omitempty"`
	// UsageMidturn asks claude_code for the adapter's raw SDK stream, which
	// is the only place a pinned runtime reports usage DURING a turn
	// (harness §7 v0.8.5, D-17) — without it the heartbeat's `usage` is zero
	// until the turn ends and the server's in-turn budget check (FR-7.3)
	// never fires. It is not free: measured on one 12.2s turn the stream cost
	// ~4× the messages and ~2× the bytes. Absent → ON. Set false to turn it
	// off machine-wide; doing it per session (only when a budget is set) is
	// backlog D-18.
	UsageMidturn *bool `json:"usage_midturn,omitempty"`
}

// UsageMidturnEnabled is UsageMidturn with its default (ON) applied. A
// pointer + this accessor rather than a plain bool: `false` and "not
// configured" have to stay distinguishable, or every daemon.json written
// before v0.8.5 would silently turn the feature off.
func (c Config) UsageMidturnEnabled() bool { return c.UsageMidturn == nil || *c.UsageMidturn }

// DefaultColabBin returns the colab binary beside the daemon executable when
// it exists, otherwise "colab" (resolved on PATH by the adapter).
func DefaultColabBin() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "colab")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "colab"
}

// DefaultPath is $COLAB_DAEMON_CONFIG or ~/.colab/daemon.json.
func DefaultPath() string {
	if p := os.Getenv("COLAB_DAEMON_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".colab", "daemon.json")
}

// Load reads the file; a missing file yields an empty Config.
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c.withDefaults(), nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c.withDefaults(), nil
}

func (c Config) withDefaults() Config {
	if c.WorkdirRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		c.WorkdirRoot = filepath.Join(home, ".colab", "work")
	}
	if c.Capacity <= 0 {
		c.Capacity = 10 // PRD §9: 데몬당 기본 10
	}
	if c.StderrDir == "" {
		c.StderrDir = filepath.Join(c.WorkdirRoot, ".colab", "logs")
	}
	if c.ColabBin == "" {
		c.ColabBin = DefaultColabBin()
	}
	return c
}

// Save writes the file with 0600 (it holds the daemon token).
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Paired reports whether pairing has happened.
func (c Config) Paired() bool { return c.RuntimeID != "" && c.DaemonToken != "" && c.ServerURL != "" }
