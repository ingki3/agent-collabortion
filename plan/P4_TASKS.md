# P4 작업 분해 — 코드 작업과 운영 안전장치 (G6 → G7)

| 항목 | 내용 |
|---|---|
| 상태 | **P4 착수(2026-09-07).** G6 통과(`plan/G6_DECISION.md`, 컷 3 통과). P4-pre 두 갈래(T-D8 스파이크 5 ‖ T-P4a 골든)부터 연다. S-50 핫픽스(T-S9a)가 P4 첫 서버 작업(T-S9) 전 조건 |
| 근거 | `PLAN.md` §3 P4·§6.2 G7·§4 스파이크 5, `EVAL.md` E13(worktree·브리프 오염·GC)·E14(오프라인·재바인딩)·E16-B, `PRD.md` FR-6.4·FR-9.2·§8.4·§8.5, SCREEN S6·S11·S13·S17, `plan/P2_BACKLOG.md`(P4 항목: D-4 worktree 삭제·rebind_prepare, S-23, S-34, S-39, D-18, K-8 가격표 결정) |
| 목표 | **`worktree` 격리와 유실 방지 + 세션 요약.** 시나리오 B(PM 스펙 → Backend/Frontend 워크트리 병렬 → diff 아티팩트 → QA 리뷰 → 수정 요청 재진입(같은 lane·같은 워크트리) → `agent_approval` → `completed` + 요약) 통과 |
| 게이트 | **G7**: 시나리오 B + `git status` 클린 + 재바인딩 + 요약. **컷 2 판정**(B 됐는데 요약 안 되면 요약을 뺀다; 재바인딩 미통과면 v1.1) |
| 예산 (PLAN §6.2) | `blocked` 8 / PR 25 |
| 동시성 | worker 동시 2, `--agent claude --model opus`; 골든은 `--agent hermes` |

## 0. 공통 규칙
P2_TASKS §0 1~10 + P3_TASKS §0 11~17 그대로. 추가 후보: 18. worktree 격리 e2e 는 **저장소 밖의 임시 git 저장소**를 만들어 쓴다(이 저장소를 대상으로 삼지 않는다 — X-2 의 worktree 판).

## 1. P4-pre — 스파이크 5 + P4a 골든 (동시)
- **T-D8 스파이크 5**(D): 추적 중 `CLAUDE.md` 에 마커 append + `git update-index --skip-worktree` → (1) Claude Code·Hermes 가 마커 구간을 읽는가 (2) 에이전트가 CLAUDE.md 를 편집하면 어떻게 되나(skip-worktree 상태에서 편집·커밋 시도) (3) 세션 종료 후 마커 구간만 복원 → `git status` 클린·원본 무손상·정당한 편집 보존. 실패면 `CLAUDE.local.md` + import 지시 우회. 보고 `plan/spikes/SPIKE_05.md`, 코드 변경 없음(있으면 별도 PR). §8.4 계약 변경 여부 Lead 판정.
- **T-P4a 골든**(Hermes): E13(worktree 준비·브랜치 이름·브리프 오염 방지·이중 쓰기·GC 판정 — 미병합 차단·브랜치 보존)·E14(오프라인 유예 7일·paused·재바인딩 remote URL 판정·diff 재적용 프롬프트)·요약(§8.5 클라이언트: refusal → 피드 오류 + completed) 골든 표, 태그 `p4golden`, 어댑터 관례 §0-8. 시뮬레이터: 데몬 kill -9 뒤 worktree 이중 쓰기 0.

## 2. P4b — 구현 (S ‖ D → C ‖ W)
- **T-S9 서버**: 세션 요약(Platform LLM §8.5 — 모델·캐싱·refusal 검사·stop_details 피드) + `session_summary` 메시지(P2 의 자리 채움), 컨텍스트 재사용(첨부 + 상한), workdir GC 스케줄러(보존 기한·용량·미병합/미커밋 차단·브랜치 보존, daemon-protocol §6 gc 명령 발행 — 이미 있음), 런타임 오프라인 유예(7일) → paused → 재바인딩(remote URL, diff 아티팩트 재적용 프롬프트)/종료, 활성 세션 걸린 런타임 삭제 차단, task_event 마스킹 옵션. 백로그 흡수: S-23·S-34·S-39·S-49·K-8 가격표 결정(추정을 실측급으로 승격할지).
- **T-D9 데몬**: `worktree` 준비(`git worktree add`, 브랜치 `colab/<session>/<agent>`), 브리프 오염 방지(스파이크 5 결론대로), probe 에 remote URL·브랜치·클린 여부, worktree 이중 쓰기 검증(P1 고아 정리), gc 의 worktree 삭제(`git worktree remove`, 브랜치 보존 — D-4 나머지), `rebind_prepare` 명령(다운로드만). 백로그 D-18(예산 미설정 세션 OFF).
- **T-C6 CLI**: `colab artifact submit --type diff`.
- **T-W5 웹**: S6 마법사 전체(격리 → 런타임 필터, 저장소 검증), S11(저장소·유예·재바인딩), S13 workdir 관리(gc 결과·refused 표시), S17 재바인딩 다이얼로그, 활동 피드 파일 편집·셸 카드 상세.

## 3. T-I4 통합 — G7 판정 자료
시나리오 B E2E(임시 저장소), `CLAUDE.md` 추적 상태에서 `git status` 클린, kill -9 뒤 이중 쓰기 0, 오프라인 7일 시뮬 → paused → 재바인딩 → diff 재적용 콜드 스타트, GC 미병합 차단 + 알림, 요약 refusal → 피드 오류 + completed. → `plan/G7_REPORT.md` → `plan/G7_DECISION.md`(컷 2).

## 4. 순서
1. G6 판정 통과 → T-D8 ‖ T-P4a. 2. 스파이크 5 결론(계약 변경?) → T-S9 ‖ T-D9 → T-C6 ‖ T-W5. 3. T-I4 → G7. 병행: Reviewer colab Hermes 프로파일 시범(리뷰 1건).
