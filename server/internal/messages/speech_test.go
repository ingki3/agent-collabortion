package messages

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// PRD FR-3.1.3's table, row by row. Classify is the one place the rules live,
// so this is the table's golden: a row whose premise is absent falls through,
// and nothing is ever inferred from what the body says.
func TestClassify_FR313Table(t *testing.T) {
	lead, res, wri := uuid.New(), uuid.New(), uuid.New()
	user := uuid.New()
	lane := uuid.New()
	trig := uuid.New()
	name := func(s string) *string { return &s }
	agentMention := func(id uuid.UUID, n string) gen.Mention {
		return gen.Mention{Kind: gen.MentionKindAgent, Id: id.String(), DisplayName: name(n)}
	}

	cases := []struct {
		name    string
		in      SpeechInput
		speech  gen.MessageSpeech
		to      []Addressee
		reports *uuid.UUID
		lane    *uuid.UUID
	}{
		{
			name:   "1 시스템 — 머리 없음",
			in:     SpeechInput{Kind: "system", AuthorType: "system"},
			speech: gen.MessageSpeechSystem, to: []Addressee{},
		},
		{
			name:   "2 HITL — 카드가 스스로 수신자를 적는다",
			in:     SpeechInput{Kind: "hitl", AuthorType: "agent", AuthorID: &res},
			speech: gen.MessageSpeechHitl, to: []Addressee{},
		},
		{
			name: "3 질문(blocked_q) — 멘션된 위임자",
			in: SpeechInput{Kind: "blocked_q", AuthorType: "agent", AuthorID: &wri,
				Mentions: []gen.Mention{agentMention(lead, "Lead")}},
			speech: gen.MessageSpeechQuestion, to: []Addressee{{Kind: "agent", ID: &lead, Name: "Lead"}},
		},
		{
			name: "3' 질문 — 멘션이 없으면 그 lane 이 기다리는 상대(waiting_for, 표 3행 후반)",
			in: SpeechInput{Kind: "blocked_q", AuthorType: "agent", AuthorID: &wri,
				WaitingForKind: "user", WaitingForID: &user, WaitingForName: "Director"},
			speech: gen.MessageSpeechQuestion, to: []Addressee{{Kind: "user", ID: &user, Name: "Director"}},
		},
		{
			name:   "3'' 질문 — 멘션도 waiting_for 도 없으면 방 전체(짐작하지 않는다)",
			in:     SpeechInput{Kind: "blocked_q", AuthorType: "agent", AuthorID: &wri},
			speech: gen.MessageSpeechQuestion, to: []Addressee{},
		},
		{
			name:   "4 요약 — 방 전체(받는 쪽 비움)",
			in:     SpeechInput{Kind: "summary", AuthorType: "system"},
			speech: gen.MessageSpeechSummary, to: []Addressee{},
		},
		{
			name: "5 답 — 질문 카드 스레드의 답글, 받는 쪽은 질문한 쪽(멘션 중복 없이)",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &lead,
				ParentKind: "blocked_q", ParentAuthorType: "agent", ParentAuthorID: &wri, ParentAuthorName: "Writer",
				Mentions: []gen.Mention{agentMention(wri, "Writer")}},
			speech: gen.MessageSpeechAnswer, to: []Addressee{{Kind: "agent", ID: &wri, Name: "Writer"}},
		},
		{
			name: "6 위임 — 이 코드 경로가 위임이라고 말할 때만(본문을 보지 않는다)",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &lead,
				Mentions:        []gen.Mention{agentMention(res, "Researcher")},
				DelegatedLaneID: &lane, DelegateTargetID: &res, DelegateTargetName: "Researcher"},
			speech: gen.MessageSpeechDelegate, to: []Addressee{{Kind: "agent", ID: &res, Name: "Researcher"}}, lane: &lane,
		},
		{
			name: "6' 같은 본문이라도 lane 이 없으면 위임이 아니다 — 요청",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &lead,
				Mentions: []gen.Mention{agentMention(res, "Researcher")}},
			speech: gen.MessageSpeechRequest, to: []Addressee{{Kind: "agent", ID: &res, Name: "Researcher"}},
		},
		{
			name: "7 보고 — 요청자가 받는 쪽에 있다",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &res,
				Mentions:         []gen.Mention{agentMention(lead, "Lead")},
				TriggerMessageID: &trig, TriggerAuthorType: "agent", TriggerAuthorID: &lead, TriggerAuthorName: "Lead"},
			speech: gen.MessageSpeechReport, to: []Addressee{{Kind: "agent", ID: &lead, Name: "Lead"}}, reports: &trig,
		},
		{
			name: "7' 보고 — 받는 쪽이 비었으면 요청자에게",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &res,
				TriggerMessageID: &trig, TriggerAuthorType: "user", TriggerAuthorID: &user, TriggerAuthorName: "서연"},
			speech: gen.MessageSpeechReport, to: []Addressee{{Kind: "user", ID: &user, Name: "서연"}}, reports: &trig,
		},
		{
			name: "7'' 요청자가 아닌 다른 에이전트에게 말하면 보고가 아니다 — 요청",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &res,
				Mentions:         []gen.Mention{agentMention(wri, "Writer")},
				TriggerMessageID: &trig, TriggerAuthorType: "agent", TriggerAuthorID: &lead, TriggerAuthorName: "Lead"},
			speech: gen.MessageSpeechRequest, to: []Addressee{{Kind: "agent", ID: &wri, Name: "Writer"}},
		},
		{
			name: "7''' 시스템이 깨운 턴(합류 통보)은 요청자가 없다 — 대화",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &res,
				TriggerMessageID: &trig, TriggerAuthorType: "system"},
			speech: gen.MessageSpeechChat, to: []Addressee{},
		},
		{
			name: "7'''' 보고 — 같이 부른 다른 에이전트는 받는 쪽이 아니다(표 7행: 요청자 한 명)",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &res,
				Mentions:         []gen.Mention{agentMention(lead, "Lead"), agentMention(wri, "Writer")},
				TriggerMessageID: &trig, TriggerAuthorType: "agent", TriggerAuthorID: &lead, TriggerAuthorName: "Lead"},
			speech: gen.MessageSpeechReport, to: []Addressee{{Kind: "agent", ID: &lead, Name: "Lead"}}, reports: &trig,
		},
		{
			name: "7v 보고를 받고 깨운 턴의 말은 보고가 아니라 요청 — 보고에 대한 보고는 없다(T-AGENTFIX B6)",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &lead,
				Mentions:         []gen.Mention{agentMention(wri, "Writer")},
				TriggerMessageID: &trig, TriggerAuthorType: "agent", TriggerAuthorID: &wri, TriggerAuthorName: "Writer",
				TriggerSpeech: "report"},
			speech: gen.MessageSpeechRequest, to: []Addressee{{Kind: "agent", ID: &wri, Name: "Writer"}},
		},
		{
			name: "7v' 멘션 없이 답해도 요청 — 받는 쪽은 보고한 쪽 한 명",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &lead,
				TriggerMessageID: &trig, TriggerAuthorType: "agent", TriggerAuthorID: &wri, TriggerAuthorName: "Writer",
				TriggerSpeech: "report"},
			speech: gen.MessageSpeechRequest, to: []Addressee{{Kind: "agent", ID: &wri, Name: "Writer"}},
		},
		{
			name: "7v'' 트리거가 요청(보고 아님)이면 그 답은 그대로 보고",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &wri,
				Mentions:         []gen.Mention{agentMention(lead, "Lead")},
				TriggerMessageID: &trig, TriggerAuthorType: "agent", TriggerAuthorID: &lead, TriggerAuthorName: "Lead",
				TriggerSpeech: "request"},
			speech: gen.MessageSpeechReport, to: []Addressee{{Kind: "agent", ID: &lead, Name: "Lead"}}, reports: &trig,
		},
		{
			name: "8 지시 — 사람이 에이전트를 멘션",
			in: SpeechInput{Kind: "text", AuthorType: "user", AuthorID: &user,
				Mentions: []gen.Mention{agentMention(lead, "Lead")}},
			speech: gen.MessageSpeechInstruct, to: []Addressee{{Kind: "agent", ID: &lead, Name: "Lead"}},
		},
		{
			name: "9 요청 — 에이전트가 다른 에이전트를 멘션",
			in: SpeechInput{Kind: "text", AuthorType: "agent", AuthorID: &lead,
				Mentions: []gen.Mention{agentMention(wri, "Writer")}},
			speech: gen.MessageSpeechRequest, to: []Addressee{{Kind: "agent", ID: &wri, Name: "Writer"}},
		},
		{
			name:   "10 메모 — /note 는 받는 쪽이 없다(기록만)",
			in:     SpeechInput{Kind: "text", AuthorType: "user", AuthorID: &user, Content: "/note 금요일 확인", Mentions: []gen.Mention{agentMention(lead, "Lead")}},
			speech: gen.MessageSpeechNote, to: []Addressee{},
		},
		{
			name:   "11 대화 — 멘션도 스레드도 없으면 방 전체(받는 쪽 비움)",
			in:     SpeechInput{Kind: "text", AuthorType: "user", AuthorID: &user, Content: "좋네요"},
			speech: gen.MessageSpeechChat, to: []Addressee{},
		},
		{
			name: "11' 대화 — 멘션이 없으면 스레드 대상이 받는 쪽",
			in: SpeechInput{Kind: "text", AuthorType: "user", AuthorID: &user, Content: "좋네요",
				ParentKind: "text", ParentAuthorType: "agent", ParentAuthorID: &res, ParentAuthorName: "Researcher"},
			speech: gen.MessageSpeechChat, to: []Addressee{{Kind: "agent", ID: &res, Name: "Researcher"}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.in)
			if got.Speech != string(c.speech) {
				t.Fatalf("speech = %q, want %q", got.Speech, c.speech)
			}
			if len(got.Addressees) != len(c.to) {
				t.Fatalf("addressees = %+v, want %+v", got.Addressees, c.to)
			}
			for i, want := range c.to {
				a := got.Addressees[i]
				if a.Kind != want.Kind || a.Name != want.Name || (a.ID == nil) != (want.ID == nil) || (a.ID != nil && *a.ID != *want.ID) {
					t.Fatalf("addressees[%d] = %+v, want %+v", i, a, want)
				}
			}
			switch {
			case c.reports == nil && got.RespondsTo != nil:
				t.Fatalf("responds_to = %v, want nil", got.RespondsTo)
			case c.reports != nil && (got.RespondsTo == nil || *got.RespondsTo != *c.reports):
				t.Fatalf("responds_to = %v, want %v", got.RespondsTo, *c.reports)
			}
			switch {
			case c.lane == nil && got.DelegatedLane != nil:
				t.Fatalf("delegated_lane = %v, want nil", got.DelegatedLane)
			case c.lane != nil && (got.DelegatedLane == nil || *got.DelegatedLane != *c.lane):
				t.Fatalf("delegated_lane = %v, want %v", got.DelegatedLane, *c.lane)
			}
		})
	}
}

// The author never addresses themselves, a repeated mention counts once, and
// `@all` is one addressee — the openapi rule for Message.addressees.
func TestClassify_AddresseeDedup(t *testing.T) {
	lead, wri := uuid.New(), uuid.New()
	name := func(s string) *string { return &s }
	got := Classify(SpeechInput{
		Kind: "text", AuthorType: "agent", AuthorID: &lead,
		Mentions: []gen.Mention{
			{Kind: gen.MentionKindAgent, Id: lead.String(), DisplayName: name("Lead")},
			{Kind: gen.MentionKindAgent, Id: wri.String(), DisplayName: name("Writer")},
			{Kind: gen.MentionKindAgent, Id: wri.String(), DisplayName: name("Writer")},
			{Kind: gen.MentionKindAll, Id: "all", DisplayName: name("all")},
		},
	})
	if len(got.Addressees) != 2 {
		t.Fatalf("addressees = %+v, want [Writer all]", got.Addressees)
	}
	if got.Addressees[0].ID == nil || *got.Addressees[0].ID != wri {
		t.Fatalf("addressees[0] = %+v, want Writer", got.Addressees[0])
	}
	if got.Addressees[1].Kind != "all" {
		t.Fatalf("addressees[1] = %+v, want all", got.Addressees[1])
	}
}
