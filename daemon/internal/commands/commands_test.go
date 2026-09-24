package commands

// 데몬의 명령 표를 계약·웹과 대조한다 — 표를 베끼되 늙히지 못하게(T-S13b 방식).
//
//   - contracts/openapi.yaml `ColabCommand` enum 16개 = All() (순서까지)
//   - contracts/colab-cli.md §3 의 툴 이름 목록 = ToolNames() (명령 16 + v0.8 별칭 room_get·room_messages)
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
	// 집합이 아니라 순서까지 — 서버 roles.all(gen.ColabCommandValues) 과 번들 allowed_commands
	// 가 이 순서라 Denied·브리프 [2] 가 서버와 같은 순서로 나온다(#324 NN1).
	if got := All(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("계약 enum 순서 %v\n데몬 표 %v", want, got)
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
	// §3 은 별칭 괄호 안에서 원래 이름을 한 번 더 부른다 — 이름 집합으로 대조한다.
	seen := map[string]bool{}
	var want []string
	for _, m := range regexp.MustCompile("`(colab_[a-z_]+)`").FindAllStringSubmatch(sec, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			want = append(want, m[1])
		}
	}
	got := ToolNames()
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(want), ",") {
		t.Fatalf("§3 툴 이름 %v\n데몬 %v", want, got)
	}
}

// 데몬 labels ↔ 웹 COMMAND_LABEL 자물쇠 (#250 리뷰 NN2 · V-1). 웹 파일을 읽어(서버 md 를
// 파싱하듯) 키 집합과 글자를 대조한다 — 16/16. 파일이 없으면 Skip 이 아니라 실패다: 이 표는
// 화면(S10 역할 카드)과 브리프 [2] 가 같은 말을 쓴다는 약속이고, 웹 없는 체크아웃은 그 약속을
// 잴 수 없다.
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
	if len(web) != len(all) || len(labels) != len(all) {
		t.Fatalf("웹 %d개, 데몬 labels %d개, All() %d개 — 셋이 같아야 한다", len(web), len(labels), len(all))
	}
	matched := 0
	for _, c := range all {
		switch {
		case web[c] == "":
			t.Errorf("%s: 웹 COMMAND_LABEL 에 없다", c)
		case labels[c] == "":
			t.Errorf("%s: 데몬 labels 에 없다", c)
		case web[c] != labels[c]:
			t.Errorf("%s: 웹 %q 데몬 %q", c, web[c], labels[c])
		default:
			matched++
		}
	}
	for c := range web {
		if !Known(c) {
			t.Errorf("%s: 웹에만 있다 — All() 에 없는 명령", c)
		}
	}
	t.Logf("labels ↔ COMMAND_LABEL %d/%d", matched, len(all))
}

// NN3 — Known 은 All() 로 판정한다. 라벨을 지워도 명령은 아는 것이고(Label 은 CLI 철자로
// 폴백), 라벨에만 있는 이름은 모르는 것이다.
func TestKnownIsAllNotLabels(t *testing.T) {
	for _, c := range all {
		if !Known(c) {
			t.Errorf("Known(%s) = false", c)
		}
	}
	if Known("brand_new") {
		t.Error("Known(brand_new) = true")
	}
	saved := labels["lane_delegate"]
	delete(labels, "lane_delegate")
	defer func() { labels["lane_delegate"] = saved }()
	if !Known("lane_delegate") {
		t.Error("라벨을 지웠다고 명령이 미지의 것이 됐다 — Known 이 labels 로 판정한다(NN3)")
	}
	if got := Label("lane_delegate"); got != "lane delegate" {
		t.Errorf("Label without a label = %q, want the CLI spelling", got)
	}
	labels["ghost_cmd"] = "유령"
	defer delete(labels, "ghost_cmd")
	if Known("ghost_cmd") {
		t.Error("labels 에만 있는 이름을 안다고 한다(NN3)")
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
	if len(d) != len(all)-2 || d[0] != "session_messages" || d[len(d)-1] != "work_propose" {
		t.Fatalf("Denied %v", d)
	}
	if len(Denied(All())) != 0 {
		t.Fatal("전부 허용이면 거부 목록이 비어야 한다")
	}
}
