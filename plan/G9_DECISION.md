# G9 판정 — 출시 판정: 시나리오 A·B·C·D CI 초록 + §11 대시보드 + 컷 발동 여부

| 항목 | 내용 |
|---|---|
| 판정 | **통과 — v1 범위 그대로 출시 가능. 컷 1·2·3 발동 없음.** |
| 근거 | `plan/G9_REPORT.md`(T-I5 PR #206) + 그 뒤 머지된 결함 수정(§2) |
| 조건 1 — CI E2E | ✅ `e2e` job 초록 — 72_A 46/0 · 73_B 48/0 · 74_C 42/0 · 75_D 22/0 · 76 perf · 77 security · 78 S7 (run 34765757987, 4m55s; 이후 모든 PR 에서 매번 초록) |
| 조건 2 — §11 대시보드 | ✅ `getWorkspaceMetrics` 10개 전부 읽힘(G9_REPORT §4, S14 「대시보드」 탭 #199) |
| 조건 3 — G8 | Director 결정으로 생략(`plan/G8_DECISION.md`) |
| 컷 | 컷 1(요약 생성)·컷 2(재바인딩)·컷 3(worktree) 모두 G5·G7 에서 성립 — 발동 사유 없음 |
| 판정자 | Lead(초안 2026-09-15) → **Director 확인 대기** |

## 1. `chk_na` 목록 — CI 초록이 가리는 것 (PR #206 리뷰 NN4)

| 스크립트 | N/A | 상태 |
|---|---|---|
| 77_security S1x(위임↔합류 루프 상한) | S-76 | **해결 PR #213** — 77_ 은 이제 chk(50/0) |
| 77_security S3d2(마스킹 title) | S-77 | **해결 PR #213** |
| 78_web_s7 DOM·스크린샷 | CI 에 브라우저 없음 | 로컬 agent-browser 7/0 — 남는다(의도) |

## 2. G9_REPORT 이후 닫힌 것

S-76·S-77·S-64·S-65(#213) · S-78 chain_depth(#216) · S-80 재개 hops(#240) · S-83 Lane.actions(#224) · S-84 리뷰어 필수(#232·#233·#234·#238) · W-12 마크다운(#229·#230) · W-15·W-16(#226·#228) · 세션 삭제 FR-2.7(#218~#222). PRD v0.17(OASIS 관찰 표, #236).

## 3. §11 실값 한계

`parallel_wallclock_reduction` 은 시나리오 A 실기 0.12(목표 0.4 미달) — 정의가 사람 대기(HITL)를 "전체"에 넣어 순차 세션에서 원래 낮다(K-17, note 에 명시). 출시 판정을 막는 값이 아니라 **관찰이 시작되는 값**이다. `blocked_response_minutes`·`hermes` 성공률은 표본 0.

## 4. 출시 전 마지막 작업(코드 아님)

1. 배포본 고정 — `make build`(install.sh 가 그 커밋을 설치, S-64) + dev 태그.
2. `orca orchestration reset` 여부(옛 dispatch 18건 `release_unknown`) — Director.

## 5. 열린 백로그 (v1.1 로)

G9 전 항목은 0. 낮음: S-71·S-75·S-79·S-82·S-85·I-1·I-2·I-3·W-13·W-14·W-17·W-18·W-20·D-26·D-27·K-14·K-15·K-16·K-18(관찰 표 구현)·K-19(역할별 행동 부분집합). 전체는 `plan/P2_BACKLOG.md`.
