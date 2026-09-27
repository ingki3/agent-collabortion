package queue

// T-HUMANMENTION R1 (harness v0.9.10): brief [5] carries the room's PEOPLE
// after the agents, and brief [2] carries the fixed line on mentioning one.
// The contract states both in Korean; the brief is English (v0.9.4 set that
// precedent for [2]), so — exactly as TestDetailRuleMatchesContract does — the
// check is by ELEMENT: every element of the contract's own sentence, read from
// contracts/harness.md, maps to a phrase the rendered text must carry.

import (
	"regexp"
	"strings"
	"testing"
)

// contractV0910 is harness.md's v0.9.10 paragraph — 「**브리프 [5] Roster 의
// 사람 줄 (v0.9.10 …)**」 up to the next bold heading.
func contractV0910(t *testing.T) string {
	t.Helper()
	c := harnessContract(t)
	i := strings.Index(c, "**브리프 [5] Roster 의 사람 줄 (v0.9.10")
	if i < 0 {
		t.Fatal("harness.md has no v0.9.10 brief [5] paragraph")
	}
	j := strings.Index(c[i+10:], "\n\n**")
	if j < 0 {
		t.Fatal("v0.9.10 paragraph has no end")
	}
	return c[i : i+10+j]
}

// The three room roles the contract names, and what the brief calls them.
var contractRoomRoles = map[string]string{
	"방장":  "room owner",
	"부방장": "room deputy",
	"멤버":  "member",
}

// 회귀 주입: humanRoleLabel 의 owner·deputy 분기를 지우면 (roles) FAIL;
// briefHumans 의 ORDER BY CASE 를 지우면 (order) FAIL; [5] 사람 줄의
// `mention://user/` 를 agent 로 바꾸면 (link) FAIL; Section2 에서
// s.HumanMention 을 지우면 (brief2) FAIL; HumanMentionRule 에서 「에이전트를
// 깨우지 않는다」 문구를 지우면 (brief2 wakes) FAIL.
func TestBriefHumanRosterMatchesContractV0910(t *testing.T) {
	entry := contractV0910(t)

	// (roles) the contract's three room roles each have a label, and the
	// labels are distinct (a person's standing must be readable).
	seen := map[string]bool{}
	for ko, en := range contractRoomRoles {
		if !strings.Contains(entry, ko) {
			t.Errorf("(roles) contract no longer names %q — update the table:\n%s", ko, entry)
		}
		got := humanRoleLabel(map[string]string{"방장": "owner", "부방장": "deputy", "멤버": "member"}[ko])
		if got != en {
			t.Errorf("(roles) humanRoleLabel for %s = %q, want %q", ko, got, en)
		}
		if seen[got] {
			t.Errorf("(roles) label %q is used twice", got)
		}
		seen[got] = true
	}
	// An unknown room_role falls back to the weakest standing, never to a
	// blank label — a line reading 「(person · )」 tells the agent nothing.
	if humanRoleLabel("brand_new_role") != "member" {
		t.Errorf("(roles) unknown role label = %q", humanRoleLabel("brand_new_role"))
	}

	// (link) the contract's link form is the user one, and it is what the
	// renderer writes (router.UserMentionLink is proven in the router tests;
	// here the point is that [5] uses THAT one, not the agent link).
	if !strings.Contains(entry, "mention://user/<id>") {
		t.Errorf("(link) contract lost the user link form:\n%s", entry)
	}

	// (order) the contract's order — 방장 → 부방장 → 멤버, 같으면 이름순.
	for _, want := range []string{"방장 → 부방장 → 멤버", "이름순"} {
		if !strings.Contains(entry, want) {
			t.Errorf("(order) contract lost %q:\n%s", want, entry)
		}
	}
	// The SQL is the only place that order lives, so read it here: the CASE
	// ranks the three roles and the tie-break is the display name.
	ord := regexp.MustCompile(`ORDER BY CASE rp\.role WHEN 'owner' THEN 0 WHEN 'deputy' THEN 1 ELSE 2 END, u\.display_name`)
	if !ord.MatchString(briefHumansSQL) {
		t.Errorf("(order) briefHumans does not order owner → deputy → member, then by name:\n%s", briefHumansSQL)
	}
	// Left-behind people are out, exactly as /cli/context humans[] is.
	if !strings.Contains(briefHumansSQL, "rp.left_at IS NULL") {
		t.Errorf("(order) briefHumans includes people who left:\n%s", briefHumansSQL)
	}
	if !strings.Contains(entry, "나간 사람 제외") {
		t.Errorf("(order) contract lost 나간 사람 제외:\n%s", entry)
	}

	// The mission's Director must NOT be marked — that is per mission and
	// would break [1]~[5] byte identity (the contract says so outright).
	if !strings.Contains(entry, "싣지 않는다") {
		t.Errorf("contract lost the 「미션 Director 는 싣지 않는다」 rule:\n%s", entry)
	}
	for _, s := range []string{HumanMentionRule, HumanMentionRuleMCP} {
		if strings.Contains(s, "Director") {
			t.Errorf("brief [2] human line names the Director: %q", s)
		}
	}

	// (brief2) the fixed line is in [2] of BOTH surfaces, right after the
	// mention-syntax line, and says the two things the contract says: the
	// same link form from [5], and that it wakes no agent.
	for kind, s := range map[string]Surface{"mcp": SurfaceFor("claude_code"), "cli_wrapper": SurfaceFor("hermes")} {
		sec := s.Section2()
		if !strings.Contains(sec, s.HumanMention) || s.HumanMention == "" {
			t.Fatalf("(brief2) %s Section2 lacks the human mention line:\n%s", kind, sec)
		}
		syntax := strings.Index(sec, "- Mention syntax:")
		if syntax < 0 || strings.Index(sec, s.HumanMention) != syntax+len("- Mention syntax: [@Name](mention://agent/<id>). Only mention session participants listed in [5].\n") {
			t.Errorf("(brief2) %s human line is not directly after the mention-syntax line:\n%s", kind, sec)
		}
		if !strings.Contains(s.HumanMention, "mention://user/") || !strings.Contains(s.HumanMention, "[5]") {
			t.Errorf("(brief2) %s human line lacks the link form or the [5] pointer: %q", kind, s.HumanMention)
		}
		// (brief2 wakes) 「사람 멘션은 알림만 보내고 에이전트를 깨우지 않는다」.
		if !strings.Contains(s.HumanMention, "notifies") || !strings.Contains(s.HumanMention, "wakes no agent") {
			t.Errorf("(brief2 wakes) %s human line does not say it only notifies and wakes no agent: %q", kind, s.HumanMention)
		}
	}
	// Per surface words (v0.9.6 rule), which the contract spells out.
	if !strings.Contains(HumanMentionRule, "`colab message post --mention @Name`") {
		t.Errorf("shell human line lost the contract's command: %q", HumanMentionRule)
	}
	if !strings.Contains(HumanMentionRuleMCP, "`colab_message_post`") || !strings.Contains(HumanMentionRuleMCP, "`mention`") {
		t.Errorf("mcp human line lost the tool and its argument: %q", HumanMentionRuleMCP)
	}
}
