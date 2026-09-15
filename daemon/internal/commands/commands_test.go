package commands

// 데몬의 명령 표를 계약·웹과 대조한다 — 표를 베끼되 늙히지 못하게(T-S13b 방식).
//
//   - contracts/openapi.yaml `ColabCommand` enum 13개 = All()
//   - contracts/colab-cli.md §3 의 툴 이름 목록 = ToolName(All())
//   - web/lib/wording.ts COMMAND_LABEL = labels (사람 말이 화면과 브리프에서 같아야 한다)

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func readRepo(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestSetMatchesOpenAPIEnum(t *testing.T) {
	text := readRepo(t, "contracts/openapi.yaml")
	i := strings.Index(text, "\n    ColabCommand:")
	if i < 0 {
		t.Fatal("openapi.yaml 에 ColabCommand 가 없다")
	}
	m := regexp.MustCompile(`enum: \[([^\]]+)\]`).FindStringSubmatch(text[i:])
	if m == nil {
		t.Fatal("ColabCommand enum 을 못 읽었다")
	}
	var want []string
	for _, s := range strings.Split(m[1], ",") {
		want = append(want, strings.TrimSpace(s))
	}
	if got := sortedCopy(All()); strings.Join(got, ",") != strings.Join(sortedCopy(want), ",") {
		t.Fatalf("계약 enum %v\n데몬 표 %v", want, All())
	}
	for _, c := range want {
		if !Known(c) || labels[c] == "" {
			t.Errorf("%s: 사람 말이 없다", c)
		}
	}
}

func TestToolNamesMatchContractSection3(t *testing.T) {
	text := readRepo(t, "contracts/colab-cli.md")
	i := strings.Index(text, "\n## 3. MCP 서버")
	if i < 0 {
		t.Fatal("colab-cli.md 에 §3 이 없다")
	}
	sec := text[i:]
	if j := strings.Index(sec, "\n## 4."); j > 0 {
		sec = sec[:j]
	}
	var want []string
	for _, m := range regexp.MustCompile("`(colab_[a-z_]+)`").FindAllStringSubmatch(sec, -1) {
		want = append(want, m[1])
	}
	var got []string
	for _, c := range All() {
		got = append(got, ToolName(c))
	}
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(want), ",") {
		t.Fatalf("§3 툴 이름 %v\n데몬 %v", want, got)
	}
}

func TestLabelsMatchWebWording(t *testing.T) {
	text := readRepo(t, "web/lib/wording.ts")
	i := strings.Index(text, "export const COMMAND_LABEL = {")
	if i < 0 {
		t.Fatal("web/lib/wording.ts 에 COMMAND_LABEL 이 없다")
	}
	block := text[i:]
	block = block[:strings.Index(block, "} as const;")]
	web := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([a-z_]+): "([^"]+)",`).FindAllStringSubmatch(block, -1) {
		web[m[1]] = m[2]
	}
	if len(web) != len(labels) {
		t.Fatalf("웹 %d개, 데몬 %d개", len(web), len(labels))
	}
	for c, l := range labels {
		if web[c] != l {
			t.Errorf("%s: 웹 %q 데몬 %q", c, web[c], l)
		}
	}
}

func TestCLINamesAndParticles(t *testing.T) {
	for cmd, want := range map[string]string{
		"lane_delegate": "lane delegate", "hitl_approve_request": "hitl approve-request",
		"hitl_request_info": "hitl request-info", "session_get": "session get", "brand_new": "brand new",
	} {
		if got := CLIName(cmd); got != want {
			t.Errorf("CLIName(%s)=%q want %q", cmd, got, want)
		}
	}
	for word, want := range map[string]string{"위임": "을", "검토 반려": "를", "완료 승인 요청": "을", "x": "을"} {
		if got := ObjectParticle(word); got != want {
			t.Errorf("ObjectParticle(%s)=%q want %q", word, got, want)
		}
	}
}

func TestEmptyMeansEverything(t *testing.T) {
	if Args(nil) != nil || EnvEntry(nil) != "" || Denied(nil) != nil {
		t.Fatal("빈 목록은 전부다 — 플래그·변수·거부 목록이 없어야 한다")
	}
	allowed := []string{"session_get", "message_post"}
	if a := Args(allowed); strings.Join(a, " ") != "--allow session_get,message_post" {
		t.Fatalf("Args %v", a)
	}
	if e := EnvEntry(allowed); e != "COLAB_ALLOWED_COMMANDS=session_get,message_post" {
		t.Fatalf("EnvEntry %q", e)
	}
	d := Denied(allowed)
	if len(d) != len(all)-2 || d[0] != "session_messages" || d[len(d)-1] != "hitl_request_info" {
		t.Fatalf("Denied %v", d)
	}
	if len(Denied(All())) != 0 {
		t.Fatal("전부 허용이면 거부 목록이 비어야 한다")
	}
}
