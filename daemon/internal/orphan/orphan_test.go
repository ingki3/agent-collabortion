package orphan

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRecordListRemove(t *testing.T) {
	s := Store{Root: t.TempDir()}
	r := Record{TaskID: "task-1", Attempt: 2, PGID: 4242, StartedAt: time.Now().UTC()}
	if err := s.Record(r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".colab", "attempts", "task-1.2.json")); err != nil {
		t.Fatal("record file missing (E11-01)")
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].PGID != 4242 || list[0].TaskID != "task-1" || list[0].Attempt != 2 {
		t.Fatalf("list %+v", list)
	}
	if err := s.Remove("task-1", 2); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("record not removed (E11-02)")
	}
	if err := s.Remove("task-1", 2); err != nil {
		t.Fatal("second remove should be a no-op")
	}
}

// E11-05 — a recorded, still-alive group is killed by Sweep; dead records
// are just dropped.
func TestSweepKillsLiveGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & sleep 60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	go cmd.Wait()
	s := Store{Root: t.TempDir(), KillAfter: 2 * time.Second}
	s.Record(Record{TaskID: "live", Attempt: 1, PGID: pgid})
	s.Record(Record{TaskID: "dead", Attempt: 1, PGID: 999999})
	if !Alive(pgid) {
		t.Fatal("test group should be alive")
	}
	swept, err := s.Sweep()
	if err != nil || len(swept) != 2 {
		t.Fatalf("%+v %v", swept, err)
	}
	for _, sw := range swept {
		if sw.Record.TaskID == "live" && !sw.Alive {
			t.Fatal("live group not detected")
		}
		if sw.Record.TaskID == "dead" && sw.Alive {
			t.Fatal("dead pgid reported alive")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for Alive(pgid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if Alive(pgid) {
		t.Fatal("group still alive after sweep")
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("records left: %+v", list)
	}
}
