/**
 * **목 문장 ↔ 서버 문장 대조**(T-W10, PR #192 후속).
 *
 * 서버(S-67)가 사용자 대면 문장 — `Problem.detail`·`title`·`errors[].message`·시스템 메시지 — 을 §8.4 의 말로
 * 바꿨다. 목이 그 문장을 **다른 말로** 흉내 내면 화면 테스트(p3-mock·p4-mock·u1)가 실서버를 대변하지 못한다.
 * 그래서 목의 문장은 `wording.ts` 한곳에 두고, 여기서 `server/` 소스의 **그 파일**에 같은 리터럴이 있는지 글자
 * 단위로 잰다. 서버가 문장을 바꾸면 이 테스트가 먼저 빨개진다 — 그때 `wording.ts` 를 따라 고친다.
 *
 * 재는 것(describe 하나 = 파일 하나, `lib/mock/server-wording/<글자>-….test.ts` — PR #212 리뷰 NN5, W-13):
 *   (a) `SERVER` 표의 항목마다 `server/<at>` 에 `text` 가 리터럴로 있다(형식 문자열은 `%d`·`%s` 그대로).
 *   (b) `TITLE`·`STATUS_LABEL`·`NOT_FOUND_NOUN` 은 `apperr.go` 의 세 표(`titles`·`statusLabels`·`NotFoundNouns`)와
 *       **항목 단위로 같다** — Go 소스를 파싱해 비교한다(하드코딩 대조 아님).
 *   (c) `josa` 는 `apperr.Josa` 와 같은 답을 낸다(받침 유무·비한글).
 *   (d) `handlers.ts` 는 표의 모든 키를 쓴다(죽은 행 없음), 옛 문장(PR #192 가 지목한 12곳의 말)은 남아 있지 않다.
 *   (e) 세션 시작 시스템 메시지 — 목·`e2e/u1.sh` 단언·서버 `sessions.go` 가 같은 머리말을 쓴다.
 *   (f) 관측 지표 10개(`METRIC_DEFS`)는 서버 `internal/metrics/metrics.go` 의 `Defs` 와 **항목 단위로 같다**(key·unit·target·
 *       target_op·label·note — Go 소스를 파싱해 비교). S14 「대시보드」가 그대로 보이는 문장이라 서버가 정한다(T-W11).
 *   (g) T-S12(#200)·T-S14(#209) 가 만든 op — 시험 대화·지표·보안 탭 403 · 멤버 역할·제거 · 알림 설정 — 의 문장은 전부 `SERVER` 에서
 *       온다. 목의 오류 code·순서·판정 조건이 서버 소스(`auth/members.go` PlanRoleChange · PlanRemoval, `handlers_members.go`,
 *       `auth/notifications.go`)와 같은지 문자열로 잰다(T-W12).
 *       `MOCK_ONLY` 는 **서버가 아직 안 만든 검증의 문장만** 담는다 — T-S17 #220 뒤 비었다가 T-W15(리뷰어 검사, 계약 #232 v0.1.4)가
 *       다시 채웠다. 미구현의 근거는 서버 소스에 `reviewer_required` 리터럴이 없다는 것이다. T-S18 이 머지되면 (h) 가 빨개지고, 그때
 *       세 문장을 T-S18 의 실제 문장으로 `SERVER` 에 옮긴다.
 *   (h) T-W15 — MOCK_ONLY 는 리뷰어 검사 넷뿐이고 문장은 T-S18(PR #233) 의 리터럴 그대로다. dev 에 그 리터럴이 오르면 글자 단위로 대조하고,
 *       아직이면 부재를 잰다. updateSession 의 immutable 두 문장은 서버(handlers_sessions_p3.go)에서 온다. (T-S18 머지 뒤 비었다.)
 *   (i) T-W16(v1.1, 서버 T-S19 와 동시) — MOCK_ONLY 는 **빈 턴 행의 문장 하나**(`empty_turn_note`, PRD FR-7.2 가 못박은 문장)뿐이고, 화면 폴백
 *       `lib/wording.ts` EMPTY_TURN.note 와 같다. 서버 소스에 그 리터럴이 오르면(T-S19 finish) 이 테스트가 빨개진다 — 그때 `SERVER` 로 옮긴다.
 *       「관찰」 표 5행의 정의(`OBSERVATION_DEFS`)도 같은 규칙: 서버에 `chain_scale` 리터럴이 생기면 (f) 처럼 Go 표를 파싱해 대조하도록 바꾼다.
 */
/** 공유 헬퍼 — 각 파일이 여기서 가져간다. 여기에는 테스트가 없다(`_shared.ts`, vitest include 밖). */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

/** 모노레포 루트 기준 경로 — `lib/mock/server-wording/` 에서 셋 위가 `web/`, 넷 위가 루트다. */
export const WEB_ROOT = join(__dirname, "..", "..", "..");
export const MOCK_DIR = join(WEB_ROOT, "lib", "mock");
export const SERVER_ROOT = join(WEB_ROOT, "..", "server");
export const CONTRACTS_ROOT = join(WEB_ROOT, "..", "contracts");
export const HANDLERS = readFileSync(join(MOCK_DIR, "handlers.ts"), "utf8");
export const MOCK_WORDING_SRC = readFileSync(join(MOCK_DIR, "wording.ts"), "utf8");
export const U1 = readFileSync(join(WEB_ROOT, "e2e", "u1.sh"), "utf8");

export function goSource(rel: string): string {
  const p = join(SERVER_ROOT, rel);
  if (!existsSync(p)) throw new Error(`server 소스가 없다: ${p} — 이 테스트는 모노레포 체크아웃(server/ 포함)에서 돈다`);
  return readFileSync(p, "utf8");
}

/** Go 의 `var name = map[K]V{ "k": "v", … }` 를 {k: v} 로. 주석 줄은 뺀다. */
export function goMap(src: string, name: string): Record<string, string> {
  const m = src.match(new RegExp(`var ${name} = map\\[[^\\]]+\\][^{]+\\{([\\s\\S]*?)\\n\\}`));
  if (!m) throw new Error(`apperr.go 에 ${name} 표가 없다`);
  const out: Record<string, string> = {};
  for (const line of m[1].split("\n")) {
    const e = line.trim().match(/^([^:/]+):\s*"([^"]*)",?$/);
    if (e) out[e[1].trim().replace(/^"|"$/g, "").replace(/^http\.Status/, "")] = e[2];
  }
  return out;
}
