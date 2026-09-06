"use client";
/**
 * /dev/components — 컴포넌트 스토리(COMPONENTS.md §2). P2 에서 Lane Card `x1YCq` · Activity Feed `h6pub` ·
 * Status Banner `zPJly` · Condition Row `XMNop` · Profile Row `ZWC8q` · 런타임 카드가 더해졌다.
 * 디자인 대조와 스크린샷(e2e/dev-shots.sh)의 대상이다.
 */
import { AgentChip } from "@/components/AgentChip";
import { AppNav } from "@/components/AppNav";
import { MessageCard } from "@/components/MessageCard";
import { ActivityRail } from "@/components/ActivityRail";
import { ActivityFeed } from "@/components/ActivityFeed";
import { Composer } from "@/components/Composer";
import { LaneCard } from "@/components/LaneCard";
import { PausedBanner } from "@/components/PausedBanner";
import { ConditionRow } from "@/components/ConditionRow";
import { AgentProfileEditor } from "@/components/AgentProfileEditor";
import { RuntimeCard } from "@/components/RuntimeCard";
import { capabilityIndex } from "@/lib/runtime-options";
import type { AgentStatus, Lane, LaneStatus, Message, PausedDetail, Runtime, TaskEvent } from "@/lib/api/types";

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
  { id: "e1", task_id: "t1", seq: 1, class: "runtime", verb: "start", object_ref: null, outcome: "cold_start", created_at: T },
  { id: "e2", task_id: "t1", seq: 2, class: "message", verb: "think", object_ref: null, outcome: "ok", created_at: T, sentence: "Lead가 계획을 생각했다 → ok" },
  { id: "e3", task_id: "t1", seq: 3, class: "tool", verb: "read", object_ref: "README.md", outcome: "ok", tool: "Read", created_at: T },
  { id: "e4", task_id: "t1", seq: 4, class: "tool", verb: "run_shell", object_ref: "npm test", outcome: "failed", created_at: T },
  { id: "e5", task_id: "t1", seq: 5, class: "status", verb: "post_message", object_ref: "m1", outcome: "ok", created_at: T },
];

const STATUSES: AgentStatus[] = ["idle", "working", "waiting_human", "error", "offline", "disabled"];

const LANE_STATES: LaneStatus[] = ["queued", "running", "waiting_human", "blocked", "paused", "done", "failed"];
const laneOf = (status: LaneStatus): Lane => ({
  id: `l-${status}`, session_id: "s1", parent_lane_id: null, agent_id: "ag1", agent_name: "Backend", profile_id: "p1",
  depends_on: [], workdir_id: null, workdir_ref: status === "running" ? "wt/backend" : null, delegated_from_task_id: null,
  has_runtime_session: status !== "running", brief: "결제 모듈의 실패 경로를 정리한다", status,
  blocked_note: status === "blocked" ? "국내만인가요, 글로벌 포함인가요?" : null,
  blocked_message_id: status === "blocked" ? "m-q" : null,
  waiting_for: status === "blocked" ? "Lead" : status === "waiting_human" ? "Director 승인 대기" : null,
  hitl_request_id: null, paused_over_usd: status === "paused" ? 1.4 : null,
  failure_kind: status === "failed" ? "timeout" : null, reentry_count: status === "done" ? 2 : 0,
  current_activity: status === "running" ? "src/payments.ts 를 고치는 중…" : null,
  queue_position: status === "queued" ? 2 : null,
  actions: status === "running" ? ["restart", "cancel"] : status === "queued" ? ["cancel"] : status === "blocked" ? ["open_question"]
    : status === "waiting_human" ? ["respond_hitl"] : status === "paused" ? ["approve_budget", "cancel"] : status === "failed" ? ["restart"] : [],
  created_at: T, updated_at: T, finished_at: status === "done" || status === "failed" ? T : null,
});

const FEED: TaskEvent[] = [
  { id: "f1", task_id: "t1", attempt: 1, seq: 1, class: "message", verb: "say", object_ref: null, outcome: "ok", payload: { kind: "text" }, tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: "Lead 가 계획을 말했다 → ok", created_at: T },
  { id: "f2", task_id: "t1", attempt: 1, seq: 2, class: "status", verb: "delegate", object_ref: "lane-2", outcome: "ok", payload: { command: "lane delegate", result_ref: "lane-2" }, tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: "Lead 가 Researcher 에게 위임했다 → ok", created_at: T },
  { id: "f3", task_id: "t1", attempt: 1, seq: 3, class: "tool", verb: "edit_file", object_ref: "src/app.ts", outcome: "ok", payload: { tool_call_id: "c1", kind: "edit", path: "src/app.ts", lines_added: 12, lines_removed: 3 }, tool: "Edit", input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: "Engineer 가 src/app.ts 를 고쳤다 → ok", created_at: T },
  { id: "f4", task_id: "t1", attempt: 1, seq: 4, class: "tool", verb: "run_shell", object_ref: "npm", outcome: "failed", payload: { tool_call_id: "c2", kind: "execute", command: "npm test", exit_code: 1 }, tool: "Bash", input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: "Engineer 가 npm test 를 돌렸다 → failed", created_at: T },
  { id: "f5", task_id: "t1", attempt: 1, seq: 5, class: "runtime", verb: "error", object_ref: null, outcome: "failed", payload: { failure_kind: "quota", detail: "쿼터 초과" }, tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: "런타임 오류 → failed", created_at: T },
  { id: "f6", task_id: "t1", attempt: 1, seq: 6, class: "tool", verb: "read", object_ref: "README.md", outcome: "started", payload: { tool_call_id: "c3", kind: "read" }, tool: "Read", input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: "README.md 를 읽는 중…", created_at: T },
];

const PAUSED: { label: string; d: PausedDetail }[] = [
  { label: "budget", d: { reason: "budget", paused_at: T, budget: { limit_usd: 20, spent_usd: 21.4 }, resolve_actions: ["resume"] } },
  { label: "time", d: { reason: "time", paused_at: T, time: { limit: "PT4H", elapsed: "PT4H12M" }, resolve_actions: ["resume"] } },
  { label: "loop · pair_roundtrips", d: { reason: "loop", paused_at: T, loop: { limit: "pair_roundtrips", count: 5, agents: ["ag-1", "ag-2"] }, resolve_actions: ["resume"] } },
  { label: "runtime_offline", d: { reason: "runtime_offline", paused_at: T, runtime: { runtime_id: "r1", offline_since: "2026-08-28T00:00:00Z" }, resolve_actions: ["rebind", "cancel"] } },
  { label: "director", d: { reason: "director", paused_at: T, resolve_actions: ["resume", "cancel"] } },
];

const RUNTIME: Runtime = {
  id: "r1", workspace_id: "w1", name: "MacBook", host: "macbook.local", status: "online", daemon_version: "0.4.0", last_seen_at: T,
  capabilities: [
    { kind: "claude_code", version: "2.1.258", adapter_version: "0.74.0", logged_in: true, models: ["claude-sonnet-5", "claude-opus-5"], protocol_version: 1, resume: true, usage: true, tool_disallow: true, brief_transport: "acp_meta_system_prompt", allow_once_missing: false, supported_options: { effort: ["low", "medium", "high", "xhigh"] } },
    { kind: "hermes", version: "0.20.6", adapter_version: null, logged_in: true, models: ["hermes-4"], protocol_version: 1, resume: false, usage: false, tool_disallow: false, brief_transport: "instruction_file", allow_once_missing: true },
  ],
  repos: [{ path: "~/dev/colab", remote_url: "git@github.com:ingki3/agent-collabortion.git", branch: "main", clean: true }],
  max_concurrent_tasks: null, running_task_count: 2, workdir_disk_bytes: 3_200_000_000,
  colab_cli: { present: true, version: "0.4.0" }, offline_since: null, grace_ends_at: null, paused_session_count: 0,
  created_at: T, updated_at: T,
};
const RUNTIME_NO_CLI: Runtime = { ...RUNTIME, id: "r2", name: "데스크탑", colab_cli: { present: false, version: "" } };
const PROFILES = [
  { id: "p1", agent_id: "a1", name: "default", runtime_kind: "claude_code" as const, model: "claude-sonnet-5", options: { effort: "high" }, env: {}, args: [], is_default: true, fallback_profile_id: "p2", created_at: T, updated_at: T },
  { id: "p2", agent_id: "a1", name: "fast", runtime_kind: "hermes" as const, model: "hermes-4", options: {}, env: {}, args: [], is_default: false, fallback_profile_id: null, created_at: T, updated_at: T },
];

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

      <section className="story" data-testid="story-feed">
        <h2>활동 피드 — 렌더 클래스 5종(x-render-class)</h2>
        <div className="story__grid">
          <div className="story__cell"><div className="story__label">5클래스 + 진행 중 · 실패 강조</div><ActivityFeed events={FEED} title="run · Claude Code · cold_start" /></div>
          <div className="story__cell"><div className="story__label">컷 1 — file_edit·shell → raw, 그 실패는 error</div><ActivityFeed events={FEED} cut1 /></div>
          <div className="story__cell"><div className="story__label">structured=false (강등)</div><ActivityFeed events={FEED} structured={false} /></div>
          <div className="story__cell"><div className="story__label">침묵 — 대기 중…</div><ActivityFeed events={[]} /></div>
        </div>
      </section>

      <section className="story" data-testid="story-lane-card">
        <h2>Lane Card — 7상태</h2>
        <div className="story__grid">
          {LANE_STATES.map((st) => (
            <div className="story__cell" key={st} style={{ maxWidth: 268 }}>
              <div className="story__label">status={st}</div>
              <LaneCard lane={laneOf(st)} onRestart={() => {}} onCancel={() => {}} onOpenQuestion={() => {}} onRespondHitl={() => {}} onApproveBudget={() => {}} />
            </div>
          ))}
          <div className="story__cell" style={{ maxWidth: 268 }}>
            <div className="story__label">권한 없음 — 숨기지 않고 비활성</div>
            <LaneCard lane={{ ...laneOf("running"), actions: [] }} onRestart={() => {}} onCancel={() => {}} disabledReason="Director·deputy 만 할 수 있습니다" />
          </div>
        </div>
      </section>

      <section className="story" data-testid="story-paused-banner">
        <h2>Status Banner — paused 사유 5종</h2>
        <div className="story__grid">
          {PAUSED.map((x) => (
            <div className="story__cell" key={x.label} style={{ maxWidth: 300 }}>
              <div className="story__label">{x.label}</div>
              <PausedBanner detail={x.d} agentName={(id) => (id === "ag-1" ? "Backend" : "QA")} onResume={async () => {}} onRebind={() => {}} onCancel={() => {}} />
            </div>
          ))}
        </div>
      </section>

      <section className="story" data-testid="story-condition-row">
        <h2>Condition Row</h2>
        <div className="story__grid">
          <div className="story__cell">
            <div className="story__label">S7 진행률</div>
            <ConditionRow type="artifact_submitted" met who="assignee" metAt={T} />
            <ConditionRow type="user_approval" met={false} nextActor="director" />
          </div>
          <div className="story__cell">
            <div className="story__label">S6 마법사</div>
            <ConditionRow type="artifact_submitted" met={null} variant="wizard" selected who="assignee" onToggle={() => {}} />
            <ConditionRow type="criteria_met" met={null} variant="wizard" disabled disabledNote="v1.1 — 성공 기준 자동 판정은 아직 없습니다" />
          </div>
        </div>
      </section>

      <section className="story" data-testid="story-profile-row">
        <h2>Profile Row — 옵션은 supported_options 가 정한다</h2>
        <div style={{ maxWidth: 620 }}>
          <AgentProfileEditor
            profiles={PROFILES}
            caps={capabilityIndex([RUNTIME])}
            canEdit
            onCreate={async () => {}}
            onUpdate={async () => {}}
            onDelete={async () => {}}
          />
        </div>
      </section>

      <section className="story" data-testid="story-runtime-card">
        <h2>런타임 카드 — 능력 새 키 · colab CLI</h2>
        <div className="story__grid">
          <div className="story__cell"><div className="story__label">colab CLI 있음</div><RuntimeCard rt={RUNTIME} /></div>
          <div className="story__cell"><div className="story__label">colab CLI 없음 — 경고</div><RuntimeCard rt={RUNTIME_NO_CLI} /></div>
        </div>
      </section>

      <section className="story" data-testid="story-composer">
        <h2>작성창 — 서버 미리보기 · new_lane 토글</h2>
        <div style={{ maxWidth: 580 }}>
          <Composer
            agents={[{ id: LEAD, name: "Lead", participant: true }, { id: "a-x", name: "X", participant: false }]}
            members={[{ id: "u1", name: "민지" }]}
            previewDelayMs={0}
            onPreview={async (i) => ({
              note_only: i.content.trimStart().startsWith("/note "),
              implicit_routing_suppressed: i.content.includes("mention://all"),
              triggers: i.content.includes(LEAD)
                ? [{ agent_id: LEAD, agent_name: "Lead", rule: 2, profile: { id: "p1", name: "default", runtime_kind: "claude_code", model: "claude-sonnet-5" }, lane: { resolution: i.newLane ? 1 : 3, lane_id: i.newLane ? null : "lane-1", reentry: false }, will_queue: !i.newLane, deferred_until: null }]
                : [],
              warnings: i.content.includes("a-x") ? [{ code: "not_participant", message: "X는 이 세션 참여자가 아닙니다 — 트리거되지 않습니다", agent_id: "a-x" }] : [],
            })}
            onSubmit={async () => []}
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
