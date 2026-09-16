# v1.1 작업 분해 — 첫 라운드 (K-18 관찰 표 · K-19 역할별 명령)

| 항목 | 내용 |
|---|---|
| 상태 | **구현 4/4 머지(2026-09-16)** — T-S19 #246 · T-W16 #247(+#248 동기화) · T-D13 #250 · T-C7 #251, 계약 #244·#249. T-I6 #253 머지(82_ 세 층 게이트 70/0 · CI e2e 3회 연속 초록 · 실기 9/0 · `plan/V11_REPORT.md`). **첫 라운드 종료(2026-09-16)** — Lead 실서버 확인: 관찰 5행 실값, reviewer 실기 1턴에서 위임 도구 부재·lane 0·거부 0. 다음 라운드 후보: D-28(capacity 창) · V-1 · W-21 · I-5. 착수 2026-09-15. v1.0.0 태그 뒤 Director: "시작해. 개발 마무리, 테스트까지 완전히 진행해." |
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

## 2. 2라운드 — 백로그 정리 (2026-09-16, Director "끝까지 쭉 진행해")

계약 #255(openapi 0.1.6: S-75 문언 · K-16 cancelLane 판정은 현재 task). 4 스트림 동시:

| 작업 | 범위 |
|---|---|
| **T-S20 서버** | K-16 · S-75 · S-79 · S-82 · S-85 · V-1 서버 · S-8 · S-14 · S-15 · S-49 · S-42 |
| **T-D14 데몬** | D-28 capacity 창 · D-27 · V-1 데몬 · D-19 · D-10 · D-26(제안만) |
| **T-W17 웹** | W-9 · W-13 · W-14 · W-17 · W-18 · W-20 · W-21 · V-1 웹 · K-16 카드 |
| **T-C8 CLI** | V-1 CLI · S-40 · I-5(84_) |

포트: S20 :8121/:5465 · D14 :8122/:5466 · W17 :3016/:3117 · C8 :8123/:5467. 3라운드 후보: K-14(workdir.id — 서버·데몬 동시 + 재측정), D-26 계약.

**2라운드 종료(2026-09-16)**: T-C8 #257 · T-D14 #258 · T-W17 #259 · T-S20 #260 — 전부 Hermes APPROVE·머지. 계약 #255. 열린 것: K-20(p3golden 마커)·W-22.

## 3. 3라운드 — K-14 workdir.id (2026-09-16)

계약 #261(daemon-protocol v0.8.3 `workdir.id`·§6 id 회신, harness v0.8.11 D-26 답). T-S21(서버: 번들 id·§6 id 찾기·K-16 409 문장) → T-D15(데몬: id 회신·index 폐기·재측정 58_/64_/07) → 웹 K-16 문장 동기화(Lead). 포트: S21 :8124/:5468 · D15 :8125/:5469.

**3라운드 종료 · v1.1 종료(2026-09-17)**: T-S21 #263 · T-D15 #265(+Lead #264 웹 문장·#266 exclude 회귀 유닛). 최종 검증(dev `371bc0e`): 실기 세션 완주 90초, 로컬 e2e ci.sh **10/10**(72_~78_·81_·82_·84_, 312s). 태그 **v1.1.0**. 열린 백로그(전부 낮음): K-20·W-22·S-86·D-29·D-26(upstream).
