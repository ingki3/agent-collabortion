/** openapi 스키마 별칭 — 화면 코드가 `components["schemas"][...]` 를 반복하지 않게. */
import type { components } from "./schema";

type S = components["schemas"];

export type User = S["User"];
export type Me = S["Me"];
export type Workspace = S["Workspace"];
export type WorkspaceWithRole = Me["workspaces"][number];
export type Member = S["Member"];
export type MemberRole = S["MemberRole"];
export type InvitePreview = S["InvitePreview"];
export type Runtime = S["Runtime"];
export type RuntimeCapability = S["RuntimeCapability"];
export type Pairing = S["Pairing"];
export type PairingStatus = Pairing["status"];
export type Agent = S["Agent"];
export type AgentStatus = S["AgentStatus"];
export type AgentRole = S["AgentRole"];
// 옛 `Session`·`SessionListItem`·`SessionCreate`·`SessionUpdate`·`SessionLimits`·`Participant` 는 v0.3.0(R4, D22)에서 계약이 지웠다 —
// 목 내부 모델만 `lib/legacy-session.ts` 에 남는다. 화면은 `Room`·`Work` 를 쓴다.
export type SessionStatus = S["SessionStatus"];
export type Message = S["Message"];
export type MessageKind = S["MessageKind"];
export type MessagePage = S["MessagePage"];
export type MessageCreate = S["MessageCreate"];
export type MessagePostResult = S["MessagePostResult"];
export type Mention = S["Mention"];
export type TaskEvent = S["TaskEvent"];
export type StreamEvent = S["StreamEvent"];
export type StreamEventType = StreamEvent["type"];
export type Problem = S["Problem"];

// ── P2 (T-W2) — S7 좌·우열 · S6(지워짐, T-R2-W4b) · S9·S10 · S11 ──
export type RespondTo = S["RespondTo"];
export type Isolation = S["Isolation"];
export type IsolationKind = S["IsolationKind"];
export type AutonomyLevel = S["AutonomyLevel"];
export type CompletionCondition = S["CompletionCondition"];
export type CompletionProgress = S["CompletionProgress"];
export type CompletionAtom = S["CompletionAtom"];
export type PauseReason = S["PauseReason"];
export type PausedDetail = S["PausedDetail"];
export type Lane = S["Lane"];
export type LaneStatus = S["LaneStatus"];
export type Task = S["Task"];
export type TaskStatus = S["TaskStatus"];
export type TaskAttempt = S["TaskAttempt"];
export type FailureKind = S["FailureKind"];
export type MessageCreate2 = S["MessageCreate"];
export type TriggerPreview = S["TriggerPreview"];
export type TriggerTarget = S["TriggerTarget"];
export type Artifact = S["Artifact"];
export type Decision = S["Decision"];
export type CostReport = S["CostReport"];
export type HitlRequest = S["HitlRequest"];
export type InboxItem = S["InboxItem"];
export type AgentProfile = S["AgentProfile"];
export type AgentProfileCreate = S["AgentProfileCreate"];
export type AgentProfileUpdate = S["AgentProfileUpdate"];
export type AgentCreate = S["AgentCreate"];
export type AgentUpdate = S["AgentUpdate"];
export type AgentTemplate = S["AgentTemplate"];
export type AgentTemplateKey = S["AgentTemplateKey"];
export type RuntimeKind = S["RuntimeKind"];
export type RuntimeRepo = S["RuntimeRepo"];
export type RuntimeCandidate = S["RuntimeCandidate"];
export type ColabCLI = S["ColabCLI"];
export type RepoCheck = S["RepoCheck"];

// ── P3 (T-W3) — S8 인박스 · HITL 카드 · S7 상단 액션 ──
export type HitlType = S["HitlType"];
export type HitlStatus = S["HitlStatus"];
export type HitlSource = S["HitlSource"];
export type HitlResponse = S["HitlResponse"];
export type InboxItemType = S["InboxItemType"];
export type InboxSeverity = S["InboxSeverity"];
export type InboxSummary = S["InboxSummary"];
export type SessionRef = S["SessionRef"];

// ── P4 (T-W5) — S6 격리·저장소 검증 · S11 유예·재바인딩 · S13 workdir · S17 ──
export type Workdir = S["Workdir"];
export type WorkdirKind = S["WorkdirKind"];
export type WorkdirStatus = S["WorkdirStatus"];
export type RuntimeDetail = S["RuntimeDetail"];
export type RuntimeStatus = S["RuntimeStatus"];

// ── P5 (T-W6) — S14 설정 8탭 + 대시보드 · S10 시험 대화 ──
export type WorkspaceSettings = S["WorkspaceSettings"];
export type WorkspaceSettingsUpdate = S["WorkspaceSettingsUpdate"];
export type LoopLimits = S["LoopLimits"];
export type BudgetPolicy = S["BudgetPolicy"];
export type ContextReusePolicy = S["ContextReusePolicy"];
export type RuntimePolicy = S["RuntimePolicy"];
export type NotificationSettings = S["NotificationSettings"];
export type SubscriptionLevel = S["SubscriptionLevel"];
export type Invite = S["Invite"];
export type MetricsReport = S["MetricsReport"];
export type Metric = S["Metric"];
export type MetricKey = Metric["key"];
export type TestChat = S["TestChat"];
export type TestChatTurn = S["TestChatTurn"];
export type TestChatStatus = S["TestChatStatus"];

// ── v1.1 (T-W16) — S14 「관찰」 표(K-18) · 역할별 colab 명령(K-19) ──
export type ObservationReport = S["ObservationReport"];
export type ObservationRow = S["ObservationRow"];
export type ObservationKey = ObservationRow["key"];
export type ColabCommand = S["ColabCommand"];

// ── v0.19 (T-R2-W1) — 방 · 미션(S5 · S25 · S18) ──
export type Room = S["Room"];
export type RoomListItem = S["RoomListItem"];
export type RoomCreate = S["RoomCreate"];
export type RoomUpdate = S["RoomUpdate"];
export type RoomStatus = S["RoomStatus"];
export type RoomRole = S["RoomRole"];
export type RoomBlockedReason = S["RoomBlockedReason"];
export type RoomVisibility = S["RoomVisibility"];
export type RoomDefaults = S["RoomDefaults"];
export type RoomParticipantRef = S["RoomParticipantRef"];
export type WorkListItem = S["WorkListItem"];
export type WorkStatus = S["WorkStatus"];
export type Work = S["Work"];
export type WorkSource = S["WorkSource"];
export type RoomParticipant = S["RoomParticipant"];
export type BlockedDetail = S["BlockedDetail"];
/**
 * `listRooms`·`listWorks` 는 계약에서 `allOf: [Page, {items: X[]}]` 라 생성 타입의 `items` 가 `unknown[] & X[]` 로 접힌다(`Page.items` 가 `{}`).
 * 봉투의 `items` 를 그 op 의 항목 타입으로 읽는다 — 모양은 계약이 정하고 이 함수는 타입만 좁힌다.
 */
export function pageItems<T>(page: { items: unknown[] }): T[] {
  return page.items as T[];
}
