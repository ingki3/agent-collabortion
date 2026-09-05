"use client";
/** /dev/components — Message Card · Agent Chip · App Nav 스토리(COMPONENTS.md §2.2, Agent Chip MmxsR, App Nav D6yb65). */
import { AgentChip } from "@/components/AgentChip";
import { AppNav } from "@/components/AppNav";
import { MessageCard } from "@/components/MessageCard";
import { ActivityRail } from "@/components/ActivityRail";
import { Composer } from "@/components/Composer";
import type { AgentStatus, Message, TaskEvent } from "@/lib/api/types";

const T = "2026-09-05T10:00:00Z";
let seq = 0; // 결정적 id — SSR/클라이언트 하이드레이션 불일치 방지
const msg = (over: Partial<Message>): Message => ({
  id: over.id ?? `m${++seq}`,
  session_id: "s1",
  author_type: "user",
  author_id: "u1",
  author: { name: "민지" },
  parent_id: null,
  content: "안녕하세요",
  mentions: [],
  source_task_id: null,
  kind: "text",
  state: "posted",
  created_at: T,
  ...over,
});

const LEAD = "a-lead";
const MESSAGES: { label: string; m: Message; replies?: Message[]; askee?: string; activity?: boolean }[] = [
  { label: "text · 사람", m: msg({ content: `[@Lead](mention://agent/${LEAD}) 인사해줘`, mentions: [{ kind: "agent", id: LEAD, display_name: "Lead" }] }) },
  {
    label: "text · 에이전트 + 활동 보기 슬롯",
    m: msg({ author_type: "agent", author_id: LEAD, author: { name: "Lead", role: "lead" }, content: "안녕하세요! 계획을 세우겠습니다.", source_task_id: "t1", reply_count: 2 }),
    replies: [msg({ id: "r1", parent_id: "root", content: "좋아요" }), msg({ id: "r2", parent_id: "root", author_type: "agent", author: { name: "Lead" }, content: "감사합니다" })],
    activity: true,
  },
  { label: "system", m: msg({ author_type: "system", author_id: null, author: undefined, kind: "system", content: "goal: 국내 B2B SaaS 결제 시장 조사 보고서 10페이지" }) },
  {
    label: "blocked_q · 질문 카드(스레드 루트) + answer 답글",
    m: msg({ author_type: "agent", author: { name: "Backend" }, kind: "blocked_q", content: "DB 스키마를 바꿔도 되나요?", mentions: [{ kind: "agent", id: LEAD, display_name: "Lead" }], reply_count: 1 }),
    replies: [msg({ id: "ans", parent_id: "q", author_type: "agent", author: { name: "Lead" }, content: "네, 마이그레이션을 추가하세요." })],
    askee: "Lead",
  },
  { label: "summary", m: msg({ author_type: "system", author_id: null, author: undefined, kind: "summary", content: "세션 완료 — 보고서 1건 제출, 승인됨." }) },
];

const EVENTS: TaskEvent[] = [
  { id: "e1", task_id: "t1", seq: 1, class: "runtime", verb: "start", object_ref: { session: "acp-1" }, outcome: "cold_start", created_at: T },
  { id: "e2", task_id: "t1", seq: 2, class: "message", verb: "think", object_ref: null, outcome: "ok", created_at: T, sentence: "Lead가 계획을 생각했다 → ok" },
  { id: "e3", task_id: "t1", seq: 3, class: "tool", verb: "read", object_ref: { path: "README.md" }, outcome: "ok", tool: "Read", created_at: T },
  { id: "e4", task_id: "t1", seq: 4, class: "tool", verb: "run_shell", object_ref: { command: "npm test" }, outcome: "failed", created_at: T },
  { id: "e5", task_id: "t1", seq: 5, class: "status", verb: "post_message", object_ref: { message: "m1" }, outcome: "ok", created_at: T },
];

const STATUSES: AgentStatus[] = ["idle", "working", "waiting_human", "error", "offline", "disabled"];

export default function ComponentsPage() {
  return (
    <main className="page" style={{ maxWidth: 1100 }}>
      <h1 className="h1">컴포넌트 스토리</h1>
      <p className="muted-3">COMPONENTS.md §2.2 Message Card · Agent Chip `MmxsR` · App Nav `D6yb65` · 작성창 · 활동 피드 원본 레일 · <a href="/dev/badges">Badge</a></p>

      <section className="story" data-testid="story-agent-chip">
        <h2>Agent Chip</h2>
        <div className="story__grid">
          {STATUSES.map((s) => (
            <div className="story__cell" key={s}>
              <div className="story__label">status={s}</div>
              <AgentChip name="Backend" role="engineer" status={s} profile="Hermes · gpt" statusNote={s === "idle" ? "lane #3 예산 대기 — 둘째 줄로 줄바꿈된다(N2)" : undefined} isAssignee={s === "working"} />
            </div>
          ))}
          <div className="story__cell">
            <div className="story__label">derive: running task → working</div>
            <AgentChip name="Lead" role="lead" derive={{ taskStatuses: ["completed", "running"] }} />
          </div>
          <div className="story__cell">
            <div className="story__label">size=sm</div>
            <AgentChip name="QA" role="reviewer" status="waiting_human" size="sm" />
          </div>
        </div>
      </section>

      <section className="story" data-testid="story-message-card">
        <h2>Message Card</h2>
        <div className="stack" style={{ maxWidth: 580 }}>
          {MESSAGES.map((x, i) => (
            <div className="story__cell" key={i}>
              <div className="story__label">{x.label}</div>
              <MessageCard message={x.m} replies={x.replies} askee={x.askee} onReply={() => {}} activity={x.activity ? <ActivityRail events={EVENTS} /> : undefined} />
            </div>
          ))}
        </div>
      </section>

      <section className="story" data-testid="story-activity">
        <h2>활동 피드 원본 레일</h2>
        <div className="story__grid">
          <div className="story__cell"><div className="story__label">structured</div><ActivityRail events={EVENTS} /></div>
          <div className="story__cell"><div className="story__label">structured=false (강등)</div><ActivityRail events={[EVENTS[0]]} structured={false} /></div>
          <div className="story__cell"><div className="story__label">empty</div><ActivityRail events={[]} /></div>
        </div>
      </section>

      <section className="story" data-testid="story-composer">
        <h2>작성창</h2>
        <div style={{ maxWidth: 580 }}>
          <Composer
            agents={[{ id: LEAD, name: "Lead", participant: true }, { id: "a-x", name: "X", participant: false }]}
            members={[{ id: "u1", name: "민지" }]}
            onSubmit={async () => [{ code: "not_participant", message: "X는 이 세션 참여자가 아님" }]}
          />
        </div>
      </section>

      <section className="story" data-testid="story-app-nav">
        <h2>App Nav</h2>
        <div className="story__grid">
          <div className="story__cell" style={{ height: 420, overflow: "hidden" }}>
            <div className="story__label">owner · inbox 3</div>
            <div style={{ height: 380 }}><AppNav workspaceName="마케팅팀" current="/sessions" inboxCount={3} showSettings userName="민지" onLogout={() => {}} /></div>
          </div>
          <div className="story__cell" style={{ height: 420, overflow: "hidden" }}>
            <div className="story__label">member · Settings 없음 · inbox 0</div>
            <div style={{ height: 380 }}><AppNav workspaceName="마케팅팀" current="/runtimes" inboxCount={0} showSettings={false} userName="서연" /></div>
          </div>
        </div>
      </section>
    </main>
  );
}
