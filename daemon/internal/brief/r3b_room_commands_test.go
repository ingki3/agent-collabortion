package brief

// T-R3b item 4 — brief [2] names the R3 room commands (colab-cli v0.8 §2.5:
// room_list · room_read for every role, work_propose for lead · custom) the
// moment the bundle's `allowed_commands` carries them. The daemon renders
// whatever the server's roles.AllowedCommands sends, so this holds before
// and after R3a adds the three names to the closed set.

import (
	"strings"
	"testing"
)

func TestRestrictCommandsNamesRoomCommands(t *testing.T) {
	lead := []string{"session_get", "session_messages", "message_post", "room_list", "room_read", "work_propose"}
	got := RestrictCommands(serverBrief, lead)
	for _, want := range []string{"`colab room list`", "`colab room read`", "`colab work propose`"} {
		if !strings.Contains(section2(t, got), want) {
			t.Errorf("[2] for a lead lacks %s:\n%s", want, got)
		}
	}
	writer := []string{"session_get", "session_messages", "message_post", "room_list", "room_read"}
	s2 := section2(t, RestrictCommands(serverBrief, writer))
	if !strings.Contains(s2, "`colab room list`, `colab room read`") {
		t.Errorf("[2] for a writer lacks the room read commands:\n%s", s2)
	}
	if strings.Contains(s2, "`colab work propose`") {
		t.Errorf("[2] offers a writer `work propose` (§2.5: lead · custom only):\n%s", s2)
	}
}
