// Every task_event the runner emits is checked against
// contracts/task_event.schema.json — enums for class/verb/outcome, and the
// CLOSED per-class payload key sets (`additionalProperties: false`).
//
// This exists because it was needed. Adding a `payload.step` for the §5
// procedure and a `payload.rpc_error` for the cold start read like harmless
// extra detail; the schema forbids both, and nothing in the daemon would ever
// have said so — the server keeps unknown keys out of its own structs and
// answers 200. A contract you can only violate silently is one you will
// violate. Every acp test's sink runs this on every event, so any new key or
// verb has to be a contract change first.
package acp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
)

type eventSchema struct {
	classes  map[string]bool
	verbs    map[string]bool
	outcomes map[string]bool
	// payloadKeys is the union of allowed keys per payload $def name.
	payloadKeys map[string]map[string]bool
}

var (
	schemaOnce sync.Once
	schemaVal  *eventSchema
	schemaErr  error
)

// payloadDefFor maps an event's class onto the $def its payload must satisfy.
// `tool` events split: a permission answer uses the `permission` $def.
func payloadDefFor(ev contracts.TaskEvent) string {
	switch ev.Class {
	case "tool":
		if ev.Verb == "permission" {
			return "permission"
		}
		return "tool"
	default:
		return ev.Class
	}
}

func loadEventSchema() (*eventSchema, error) {
	schemaOnce.Do(func() {
		path := filepath.Join("..", "..", "..", "..", "contracts", "task_event.schema.json")
		b, err := os.ReadFile(path)
		if err != nil {
			schemaErr = err
			return
		}
		var doc struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
			Defs map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			schemaErr = err
			return
		}
		set := func(name string) map[string]bool {
			m := map[string]bool{}
			for _, v := range doc.Properties[name].Enum {
				m[v] = true
			}
			return m
		}
		s := &eventSchema{classes: set("class"), verbs: set("verb"), outcomes: set("outcome"),
			payloadKeys: map[string]map[string]bool{}}
		for name, d := range doc.Defs {
			keys := map[string]bool{}
			for k := range d.Properties {
				keys[k] = true
			}
			s.payloadKeys[name] = keys
		}
		if len(s.classes) == 0 || len(s.verbs) == 0 || len(s.payloadKeys) == 0 {
			schemaErr = fmt.Errorf("task_event.schema.json parsed empty")
			return
		}
		schemaVal = s
	})
	return schemaVal, schemaErr
}

// checkEvent returns the schema violations of one event, if any.
func checkEvent(ev contracts.TaskEvent) []string {
	s, err := loadEventSchema()
	if err != nil {
		return []string{"schema: " + err.Error()}
	}
	var bad []string
	if !s.classes[ev.Class] {
		bad = append(bad, fmt.Sprintf("class %q is not in the enum", ev.Class))
	}
	if !s.verbs[ev.Verb] {
		bad = append(bad, fmt.Sprintf("verb %q is not in the enum", ev.Verb))
	}
	if !s.outcomes[ev.Outcome] {
		bad = append(bad, fmt.Sprintf("outcome %q is not in the enum", ev.Outcome))
	}
	def := payloadDefFor(ev)
	allowed, ok := s.payloadKeys[def]
	if !ok {
		if len(ev.Payload) > 0 {
			bad = append(bad, fmt.Sprintf("class %q has no payload $def but carries %d keys", ev.Class, len(ev.Payload)))
		}
		return bad
	}
	for k := range ev.Payload {
		if !allowed[k] {
			bad = append(bad, fmt.Sprintf("payload key %q is not in the %q $def (additionalProperties:false)", k, def))
		}
	}
	return bad
}

// The schema file is reachable and the parse found real content — otherwise
// every check below would pass vacuously.
func TestTaskEventSchemaIsLoaded(t *testing.T) {
	s, err := loadEventSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !s.classes["runtime"] || !s.verbs["cancel"] || !s.outcomes["cold_start"] {
		t.Fatalf("schema enums look wrong: %+v", s.classes)
	}
	if !s.payloadKeys["runtime"]["detail"] || s.payloadKeys["runtime"]["step"] {
		t.Fatalf("runtime payload keys = %v", s.payloadKeys["runtime"])
	}
	// And the check actually rejects something.
	if bad := checkEvent(contracts.TaskEvent{Class: "runtime", Verb: "cancel", Outcome: "info",
		Payload: map[string]any{"step": "x"}}); len(bad) == 0 {
		t.Fatal("checkEvent accepted a key the schema forbids")
	}
}
