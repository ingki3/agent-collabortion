# v1.1 작업 분해 — 첫 라운드 (K-18 관찰 표 · K-19 역할별 명령)

| 항목 | 내용 |
|---|---|
| 상태 | **착수(2026-09-15).** v1.0.0 태그 뒤 Director: "시작해. 개발 마무리, 테스트까지 완전히 진행해." |
| 근거 | `PRD.md` v0.18 FR-1.9.1·FR-7.2·§11 관찰 표, `contracts/openapi.yaml` 0.1.5(`getWorkspaceObservations`·`ColabCommand`·`allowed_commands`), `colab-cli.md` v0.6 §2.5, `daemon-protocol.md` v0.8.2, `harness.md` v0.8.10, `plan/P2_BACKLOG.md` K-18·K-19·I-3 |
| 게이트 | 없음(v1.1 첫 라운드). 판정은 각 PR 의 CI + Hermes 리뷰 + 실서버 대조(Lead) |

## 0. 공통 규칙

P2~P5 §0 그대로. e2e 번호 **81_ 부터**(e2e/p5/, v1.1 도 같은 폴더). 스택 포트: T-S19 :8117/pg :5461(colab-pg-s19), T-W16 :3016/:3117, T-D13 :8118/:5462(colab-pg-d13), T-C7 :8119/:5463(colab-pg-c7), T-I6 :8120/:5464(colab-pg-i6, web :3022).

## 1. 작업

| 작업 | 범위 | 순서 |
|---|---|---|
| **T-S19 서버** | `getWorkspaceObservations` 5행 SQL · 빈 턴 기록(finish) · `allowed_commands` 파생(Agent·CliContext·번들) + `403 command_not_allowed` 강제(x-colab-cli op 전부) | 1차 ‖ T-W16 |
| **T-W16 웹** | S14 대시보드 아래 「관찰」 표 · S10 역할 카드에 허용 명령 목록(읽기 전용) · 빈 턴 정보 카드 렌더 | 1차 ‖ T-S19 |
| **T-D13 데몬** | 번들 `allowed_commands` → MCP 서버 `--allow` · 래퍼 `COLAB_ALLOWED_COMMANDS` · 브리프 [2] 허용 명령만 · acpfake 대조 | 2차(T-S19 뒤) ‖ T-C7 |
| **T-C7 CLI** | `getCliContext.allowed_commands` 캐시 → exit 3 `command_not_allowed` · `colab mcp serve --allow` 툴 등록 필터 · `COLAB_ALLOWED_COMMANDS` | 2차 ‖ T-D13 |
| **T-I6 통합** | e2e 81_(관찰 표 실값·빈 턴 카드) · 82_(역할별 명령 — reviewer 가 delegate 하면 세 층 모두 거부) CI 편입 · I-3 비용 한 줄 · 실기 대조 1회 | 3차 |
