package acpprobe

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Recorder appends JSONL records: {"t": RFC3339Nano, "kind": ..., "data": ...}.
// kind ∈ spawn · exit · send · recv · recv_garbage · permission_decision ·
// turn_start · turn_end · turn_error · note.
type Recorder struct {
	mu sync.Mutex
	f  *os.File
	n  int64
}

func NewRecorder(path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Recorder{f: f}, nil
}

type record struct {
	T    string `json:"t"`
	Seq  int64  `json:"seq"`
	Kind string `json:"kind"`
	Data any    `json:"data,omitempty"`
}

func (r *Recorder) Write(kind string, data any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	b, err := json.Marshal(record{T: time.Now().UTC().Format(time.RFC3339Nano), Seq: r.n, Kind: kind, Data: data})
	if err != nil {
		b, _ = json.Marshal(record{T: time.Now().UTC().Format(time.RFC3339Nano), Seq: r.n, Kind: "marshal_error", Data: err.Error()})
	}
	_, _ = r.f.Write(append(b, '\n'))
}

// Note writes a free-form marker (scenario boundaries, observations).
func (r *Recorder) Note(msg string, kv map[string]any) {
	if kv == nil {
		kv = map[string]any{}
	}
	kv["msg"] = msg
	r.Write("note", kv)
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	return r.f.Close()
}
