package roles

import (
	"github.com/ingki3/agent-collabortion/server/internal/cards"

	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// The role table is not typed here twice. contracts/colab-cli.md §2.5 is
// parsed — header row for the role groups, body rows for the commands and
// their ✓ / — cells — and compared with AllowedCommands for every role. A
// contract edit that this package does not follow fails here; so does a
// change here the contract does not know (T-S13b: the sentence is read from
// the contract file, not retyped in the test).

// contractTable is §2.5 as role → set of allowed commands.
func contractTable(t *testing.T) map[gen.AgentRole]map[gen.ColabCommand]bool {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, "..", "..", "..", "contracts", "colab-cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "### 2.5 ")
	if start < 0 {
		t.Fatal("colab-cli.md has no §2.5")
	}
	src = src[start:]
	if end := strings.Index(src, "\n## "); end > 0 {
		src = src[:end]
	}
	var lines []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "|") {
			lines = append(lines, l)
		}
	}
	if len(lines) < 3 {
		t.Fatalf("§2.5 table has %d rows", len(lines))
	}
	cells := func(l string) []string {
		parts := strings.Split(strings.Trim(l, "|"), "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	header := cells(lines[0])[1:] // "lead", "researcher·writer·engineer", "reviewer", "custom"
	groups := make([][]gen.AgentRole, len(header))
	for i, h := range header {
		for _, r := range strings.Split(h, "·") {
			groups[i] = append(groups[i], gen.AgentRole(strings.TrimSpace(r)))
		}
	}
	code := regexp.MustCompile("`([a-z_]+)`")
	oldName := regexp.MustCompile("옛 `[a-z_]+`")
	out := map[gen.AgentRole]map[gen.ColabCommand]bool{}
	for _, l := range lines[2:] { // skip header and the |---| rule
		c := cells(l)
		if len(c) != len(header)+1 {
			t.Fatalf("§2.5 row has %d cells, want %d: %s", len(c), len(header)+1, l)
		}
		var cmds []gen.ColabCommand
		// v0.9.10: 「(옛 `lane_delegate`)」 names the command a row replaced,
		// not a command of the row.
		first := oldName.ReplaceAllString(c[0], "")
		for _, m := range code.FindAllStringSubmatch(first, -1) {
			cmds = append(cmds, gen.ColabCommand(m[1]))
		}
		if len(cmds) == 0 {
			t.Fatalf("§2.5 row names no command: %s", l)
		}
		for i, cell := range c[1:] {
			allowed := cell == "✓"
			if !allowed && cell != "—" {
				t.Fatalf("§2.5 cell %q is neither ✓ nor —: %s", cell, l)
			}
			for _, role := range groups[i] {
				if out[role] == nil {
					out[role] = map[gen.ColabCommand]bool{}
				}
				for _, cmd := range cmds {
					out[role][cmd] = allowed
				}
			}
		}
	}
	return out
}

func TestAllowedCommandsMatchContractTable(t *testing.T) {
	table := contractTable(t)
	roles := []gen.AgentRole{gen.Lead, gen.Researcher, gen.Writer, gen.Engineer, gen.Reviewer, gen.Custom}
	if len(table) != len(roles) {
		t.Errorf("§2.5 covers %d roles, want the AgentRole enum's %d", len(table), len(roles))
	}
	// Every enum command appears in the table exactly once per role.
	for _, role := range roles {
		row := table[role]
		if row == nil {
			t.Fatalf("§2.5 has no column for role %s", role)
		}
		for _, cmd := range All() {
			if _, ok := row[cmd]; !ok {
				t.Errorf("§2.5 does not mention %s for %s", cmd, role)
			}
		}
		for cmd := range row {
			if !cmd.Valid() {
				t.Errorf("§2.5 names %q, which is not a ColabCommand", cmd)
			}
		}
		// The table's row for this role, in §2 order, must be what the server
		// hands out — and Allows must agree with it cell by cell.
		var want []gen.ColabCommand
		for _, cmd := range All() {
			if row[cmd] {
				want = append(want, cmd)
			}
		}
		got := AllowedCommands(role)
		if !slices.Equal(got, want) {
			t.Errorf("AllowedCommands(%s) = %v\n  §2.5 says            %v", role, got, want)
		}
		for cmd, allowed := range row {
			if Allows(role, cmd) != allowed {
				t.Errorf("Allows(%s, %s) = %v, §2.5 says %v", role, cmd, !allowed, allowed)
			}
		}
	}
}

func TestAllCommandsIsTheClosedEnum(t *testing.T) {
	// All() is the generated list; the generated list must be the contract's
	// enum, in the contract's order — a stale enum_values.gen.go (the script
	// not rerun after a contract edit) shows up here.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, "..", "..", "..", "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^    ColabCommand:\n(?:      [^\n]*\n)*?      enum: \[([^\]]*)\]`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("openapi.yaml has no ColabCommand enum")
	}
	var want []gen.ColabCommand
	for _, v := range strings.Split(m[1], ",") {
		want = append(want, gen.ColabCommand(strings.TrimSpace(v)))
	}
	got := All()
	if !slices.Equal(got, want) {
		t.Errorf("All() = %v\n  openapi says %v — rerun scripts/gen_enum_values.sh", got, want)
	}
	for _, c := range got {
		if !c.Valid() {
			t.Errorf("%s is not a valid ColabCommand", c)
		}
	}
	// All() hands out a copy: a caller cannot reorder the table.
	got[0] = "x"
	if All()[0] == "x" {
		t.Error("All() aliases the package list")
	}
	if len(AllowedCommands(gen.Lead)) != len(want) || len(AllowedCommands(gen.Custom)) != len(want) {
		t.Errorf("lead and custom get everything: lead %d custom %d of %d", len(AllowedCommands(gen.Lead)), len(AllowedCommands(gen.Custom)), len(want))
	}
	if s := AllowedCommandStrings(gen.Reviewer); len(s) != 15 || s[0] != "room_get" {
		t.Errorf("AllowedCommandStrings(reviewer) = %v", s)
	}
}

func TestCLIName(t *testing.T) {
	for cmd, want := range map[gen.ColabCommand]string{
		gen.ColabCommandCardDelegate: "card delegate", gen.ColabCommandCardReport: "card report", gen.ColabCommandRoomGet: "room get", gen.ColabCommandReviewApprove: "review approve",
		gen.ColabCommandHitlApproveRequest: "hitl approve-request", gen.ColabCommandHitlRequestInfo: "hitl request-info", gen.ColabCommandHitlAsk: "hitl ask",
		gen.ColabCommandRoomList: "room list", gen.ColabCommandRoomRead: "room read", gen.ColabCommandWorkPropose: "work propose",
	} {
		if got := CLIName(cmd); got != want {
			t.Errorf("CLIName(%s) = %q, want %q", cmd, got, want)
		}
	}
}

// TestQuestionTableMatchesContract is colab-cli.md §2.5 v0.9.10's 질문 표
// (the bullet after the role table) against cards.QuestionCommands — the one
// table the bundle, getCliContext and the server gate read (AllowsFor).
func TestQuestionTableMatchesContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "colab-cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "질문 표: ")
	if i < 0 {
		t.Fatal("colab-cli.md §2.5 has no 질문 표")
	}
	line := src[i:]
	line = line[:strings.Index(line, "\n")]
	line = line[:strings.Index(line, "거부 문장")]
	// The table is the list up to 「. 」 (the sentence after it talks about
	// allowed_commands); `status_set`'s value note is in parentheses.
	list := line[:strings.Index(line, ". ")]
	list = regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(list, "")
	var got []string
	for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(list, -1) {
		got = append(got, m[1])
	}
	if !slices.Equal(got, cards.QuestionCommands) {
		t.Errorf("질문 표 = %v\n  cards.QuestionCommands = %v", got, cards.QuestionCommands)
	}
	if !strings.Contains(line, "`status_set`(값 `working`·`blocked` 만)") {
		t.Errorf("질문 표 status_set 값 필터 문구가 바뀌었다: %s", line)
	}
	// role ∩ question table.
	for _, role := range []gen.AgentRole{gen.Lead, gen.Researcher, gen.Reviewer, gen.Custom} {
		q := AllowedFor(role, cards.KindQuestion)
		for _, c := range q {
			if !cards.InQuestionTable(string(c)) || !Allows(role, c) {
				t.Errorf("%s question list has %s", role, c)
			}
		}
		if slices.Contains(q, gen.ColabCommandArtifactSubmit) || slices.Contains(q, gen.ColabCommandCardDelegate) {
			t.Errorf("%s question list = %v", role, q)
		}
		if !slices.Equal(AllowedFor(role, cards.KindCard), AllowedCommands(role)) || !slices.Equal(AllowedFor(role, cards.KindNormal), AllowedCommands(role)) {
			t.Errorf("%s: only a question narrows the list", role)
		}
	}
	// reviewer has no artifact_submit in its role row; the question table
	// has none either — the intersection is 10 minus what the role lacks.
	if n := len(AllowedFor(gen.Lead, cards.KindQuestion)); n != len(cards.QuestionCommands) {
		t.Errorf("lead question list = %d, want %d", n, len(cards.QuestionCommands))
	}
}
