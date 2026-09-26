/**
 * 목이 흉내 내는 **서버 문장** — T-R2-W4a(S8 방 층 승인 · S15 활동 로그 · 알림 구독 3층). 규칙은 `./wording.ts` 의 `SERVER` 와 같다:
 * 문장은 여기서만, `server-wording/k-r2w4a.test.ts` 가 각 항목을 `server/<at>` 소스와 **글자 단위로** 대조한다.
 *
 * `./wording.ts` 와 나눈 이유는 T-R2-W3 의 `RD_SERVER` 와 같다(다른 웹 워커가 같은 시기에 `SERVER` 끝을 늘린다).
 * 이미 `SERVER` 에 있는 문장(`idempotency_key_required` · `not_approver`)은 거기서(`W`) 가져다 쓴다.
 */
import type { ServerSentence } from "./wording";

export const R4_SERVER = {
  // ── 방장 승인 요청(internal/httpapi/handlers_hitl.go — forbiddenRespond · validateHitlResponse) ──
  room_owner_not_yet: { text: "방장 응답 대기 중 · %s부터 승인 가능", at: "internal/httpapi/handlers_hitl.go" },
  approved_required: { text: "승인 요청에는 승인 또는 거절을 골라 주세요", at: "internal/httpapi/handlers_hitl.go" },
  reject_reason: { text: "거절할 때는 사유를 적어 주세요 — 결정 기록에 남습니다", at: "internal/httpapi/handlers_hitl.go" },
  answer_required: { text: "답을 입력해 주세요", at: "internal/httpapi/handlers_hitl.go" },
  not_an_option: { text: "보기 중 하나의 저장소를 골라 주세요", at: "internal/httpapi/handlers_hitl.go" },
  // ── 방 기본값 · 다른 방 읽기(internal/httpapi/handlers_settings.go — validateSettings) ──
  min_one: { text: "1 이상이어야 합니다", at: "internal/httpapi/handlers_settings.go" },
  min_500: { text: "500 이상이어야 합니다", at: "internal/httpapi/handlers_settings.go" },
  room_isolation: { text: "새 방의 격리 기본값은 없음이나 워크트리만 고를 수 있습니다", at: "internal/httpapi/handlers_settings.go" },
  supervised_unsupported: { text: "감독 모드는 아직 지원하지 않습니다", at: "internal/httpapi/handlers_settings.go" },
  // ── 미션 구독(internal/httpapi/handlers_works.go — SetWorkSubscription) ──
  work_sub_enum: { text: "구독은 전부 · 확인 요청만 · 종료만 중 하나입니다", at: "internal/httpapi/handlers_works.go" },
  // ── 방 구독(internal/httpapi/handlers_rooms.go — SetRoomSubscription, T-S-r2 #309) ──
  room_sub_enum: { text: "방 구독은 전부 · 내가 참여한 미션만 · 확인 요청만 · 끄기 중 하나입니다", at: "internal/httpapi/handlers_rooms.go" },
} as const satisfies Record<string, ServerSentence>;

/**
 * 서버에 없는 문장 — 서버 `setLaneSubscription` 은 본문 오류를 decodeJSON 공통 문장으로 낸다(여기서는 칸 이름을 적는 목 문장).
 * 시드(`/__mock/…`) 전용 문장도 여기 — 서버에 없는 경로다. 서버가 같은 문장을 만들면 k-r2w4a 가 빨개져 R4_SERVER 로 옮기라고 알린다.
 */
export const R4_MOCK_ONLY = {
  lane_sub_required: "켜기 또는 끄기를 골라 주세요",
  seed_no_room: "방이 없습니다",
} as const satisfies Record<string, string>;

export const RW4: { readonly [K in keyof typeof R4_SERVER]: (typeof R4_SERVER)[K]["text"] } = Object.fromEntries(
  Object.entries(R4_SERVER).map(([k, v]) => [k, v.text]),
) as never;
