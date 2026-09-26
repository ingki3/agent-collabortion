/**
 * 옛 세션 모양(v0.2.x `Session` 스키마) — **API 가 아니다**. v0.3.0(R4, openapi D22)에서 계약이 이 스키마들을 지웠다.
 * 목(lib/mock)이 방의 뒷받침 상태를 이 모양으로 들고 있어서(방 id = 옛 세션 id), 그 내부 모델의 타입으로만 남긴다.
 * 화면 코드는 이 파일을 API 응답 타입으로 쓰지 않는다 — 방·미션(`Room`·`Work`)을 쓴다.
 * 내용은 v0.2.13 schema.d.ts 의 components.schemas 에서 그대로 옮겼다(참조만 이 파일 안으로 돌렸다).
 */
import type { components } from "@/lib/api/schema";

type S = components["schemas"];

export type Session = {
    /** Format: uuid */
    id: string;
    /** Format: uuid */
    workspace_id: string;
    title: string;
    goal: string;
    acceptance_criteria: string[];
    /** Format: uuid */
    director_user_id: string;
    director?: S["User"];
    /** Format: uuid */
    deputy_director_user_id: string | null;
    deputy_director?: S["User"];
    /** Format: uuid */
    assignee_agent_id: string | null;
    /**
     * Format: uuid
     * @description none 격리에서 자동 선택이면 첫 dispatch까지 null(M10).
     */
    runtime_id: string | null;
    runtime?: S["Runtime"];
    isolation: S["Isolation"];
    completion_condition: S["CompletionCondition"];
    completion_progress: S["CompletionProgress"];
    limits: SessionLimits;
    autonomy: S["AutonomyLevel"];
    context_reuse_override?: S["ContextReusePolicy"];
    status: S["SessionStatus"];
    paused_reason: S["PauseReason"] | null;
    paused_detail?: S["PausedDetail"];
    cost_usd: number;
    /** @description 추정치 포함(FR-7.3 배지). */
    cost_estimated?: boolean;
    participants?: Participant[];
    context?: SessionContext[];
    /**
     * @description 호출자의 세션 역할(FR-5.3). TaskToken이면 `member`.
     * @enum {string}
     */
    my_role: "director" | "deputy" | "member";
    subscription?: S["SubscriptionLevel"];
    /** Format: uuid */
    created_by: string;
    /** Format: date-time */
    created_at: string;
    /** Format: date-time */
    updated_at: string;
    /** Format: date-time */
    started_at?: string | null;
    /** Format: date-time */
    finished_at?: string | null;
    /** Format: date-time */
    last_activity_at?: string | null;
};

export type SessionContext = {
    /** Format: uuid */
    id: string;
    type: ContextType;
    /** @description url · storage ref · session id. */
    ref: string;
    summary?: string | null;
    /** Format: date-time */
    created_at?: string;
};

export type SessionContextCreate = {
    type: ContextType;
    ref: string;
};

export type SessionCreate = {
    title: string;
    goal: string;
    /** @default [] */
    acceptance_criteria?: string[];
    /**
     * Format: uuid
     * @description 기본 생성자.
     */
    director_user_id?: string;
    /** Format: uuid */
    deputy_director_user_id?: string | null;
    /**
     * Format: uuid
     * @description worktree · container면 필수. none이면 null = "자동 선택(첫 실행 시 고정)".
     */
    runtime_id?: string | null;
    isolation: S["Isolation"];
    participants: {
        /** Format: uuid */
        agent_id: string;
        /**
         * Format: uuid
         * @description 비우면 기본 프로파일.
         */
        profile_id?: string | null;
    }[];
    /**
     * Format: uuid
     * @description 기본 담당(보통 lead). 생략하면 첫 참여자.
     */
    assignee_agent_id?: string;
    /** @default [] */
    context?: SessionContextCreate[];
    completion_condition?: S["CompletionCondition"];
    limits?: SessionLimits;
    /** @default guided */
    autonomy?: S["AutonomyLevel"];
    context_reuse_override?: S["ContextReusePolicy"];
    /**
     * @description true면 `draft`로 저장만(초기 task 없음).
     * @default false
     */
    draft?: boolean;
};

export type SessionLimits = {
    budget_usd?: number | null;
    budget_tokens?: number | null;
    /** @description ISO 8601 duration(예 `PT4H`). */
    time_limit?: string | null;
    max_tasks?: number | null;
    /** @default 5 */
    max_parallel_lanes?: number;
};

export type SessionListItem = {
    /** Format: uuid */
    id: string;
    title: string;
    goal: string;
    status: S["SessionStatus"];
    paused_reason: S["PauseReason"] | null;
    director: S["User"];
    participants: {
        /** Format: uuid */
        agent_id: string;
        name: string;
        /** Format: uri */
        avatar_url?: string | null;
    }[];
    completion_progress: {
        met: number;
        total: number;
    };
    cost_usd: number;
    budget_usd?: number | null;
    cost_estimated?: boolean;
    /** @description 주의 배지 — HITL 대기 N · blocked N · 실패 N. */
    attention: {
        hitl_open: number;
        blocked: number;
        failed: number;
    };
    running_lane_count: number;
    /** Format: uuid */
    runtime_id?: string | null;
    /** Format: date-time */
    last_activity_at: string | null;
    /** Format: date-time */
    created_at: string;
};

export type SessionUpdate = {
    title?: string;
    goal?: string;
    acceptance_criteria?: string[];
    /** Format: uuid */
    deputy_director_user_id?: string | null;
    limits?: SessionLimits;
    autonomy?: S["AutonomyLevel"];
    context_reuse_override?: S["ContextReusePolicy"];
    /** @description draft에서만. */
    completion_condition?: S["CompletionCondition"];
    /** @description draft에서만. */
    isolation?: S["Isolation"];
    /**
     * Format: uuid
     * @description draft에서만.
     */
    runtime_id?: string | null;
};

export type Participant = {
    /** Format: uuid */
    session_id: string;
    /** Format: uuid */
    agent_id: string;
    agent: {
        /** Format: uuid */
        id: string;
        name: string;
        role: S["AgentRole"];
        role_description: string;
        /** Format: uri */
        avatar_url?: string | null;
        respond_to?: S["RespondTo"];
    };
    profile: S["AgentProfile"];
    status: S["AgentStatus"];
    /** @description 칩 둘째 줄(예 "lane #2 질문 대기"). 상태 값이 아니다(N2). */
    status_note?: string | null;
    is_assignee: boolean;
    /** @description `[@이름](mention://agent/<id>)` — 로스터 붙여넣기용(FR-3.2). */
    mention_link?: string;
    /** @description 예 "프로파일의 runtime_kind가 세션 런타임에 없음". */
    warnings?: string[];
    /** Format: date-time */
    joined_at: string;
};

export type ContextType = "doc" | "url" | "file" | "session";
