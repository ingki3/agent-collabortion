package config

import (
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "daemon.json")
	c, err := Load(p)
	if err != nil || c.Paired() || c.Capacity != 10 || c.WorkdirRoot == "" {
		t.Fatalf("%+v %v", c, err)
	}
	c.ServerURL, c.RuntimeID, c.DaemonToken = "http://s", "rt_1", "cdt_x"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(p)
	if err != nil || !c2.Paired() || c2.DaemonToken != "cdt_x" {
		t.Fatalf("%+v %v", c2, err)
	}
}
