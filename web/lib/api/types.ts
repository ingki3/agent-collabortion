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
export type Session = S["Session"];
export type SessionListItem = S["SessionListItem"];
export type SessionCreate = S["SessionCreate"];
export type SessionStatus = S["SessionStatus"];
export type Participant = S["Participant"];
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
