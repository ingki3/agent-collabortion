# G5 판정 — P2 게이트: 시나리오 A 8단계 + Hermes + 템플릿 3분 (G5 → P3/G6)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G5** — "시나리오 A **8단계** + **Hermes** + **템플릿 3분 Director 실측**". 예산이 가장 큰 게이트. Hermes 미통과면 Reviewer의 colab Hermes 전환(§10.1)도 미룬다 |
| 근거 | `plan/G5_REPORT.md`(Integrator T-I2 2부, **1판·2판 PR #100 · 재측정 §10 PR #105**, 각각 Hermes APPROVE — 수치를 `out/`·DB에서 독립 재계산), `e2e/p2/30~34_*.sh` 재현 스크립트, 통합이 드러낸 결함의 수정 PR **#97(D-7)·#103(S-24~S-31)**, 계약 PR **#94·#96(harness v0.8/v0.8.1 `tool_surface`)·#101(승인 op P2·blocked_q 멘션·기상 인용)**, P3-prep #93·#95 |
| 작성 | Lead 2026-09-06 |
| 상태 | **✅ G5 통과 — 조건부 확정(2026-09-06).** 다섯 항목이 실기에서 **PASS 162 · FAIL 0 · N/A 2**(재측정). 남은 칸은 **(e) 템플릿 3분의 사람 실측 하나**로, 기계 하한 10초·예상 2~2.5분이며 절차는 `G5_REPORT.md §6.3`이다. Director 위임("알아서 진행")에 따라 Lead는 이 칸을 **Director 실측 항목으로 남긴 채 P3를 연다** — 실측이 3분을 넘기면 그 시점에 온보딩 화면(S6·S9)을 P3 안에서 고친다(§5) |

## 1. DoD 판정 (런타임: Claude Code 2.1.258 + 어댑터 0.74.0, **Hermes 0.20.6**, haiku; 스택 dev `ada78a0`)

| # | DoD | 판정 | 수치 |
|---|---|---|---|
| a | 시나리오 A **8단계 끝까지** — 위임 3 → lane 3 병렬 → 합류 → 종합 → Writer 초안 → `artifact_submitted` → **승인** → `completed` + 요약 | **통과** | 1~6단계는 G4(API 32/32)에 이어 Hermes 혼합 실행(30_)에서도 섰다. 7·8단계 `33_approval_completed.sh` **41/0 (N/A 2)**: 플랫폼 발행 `approval` HITL(`source=system`·`task_id` NULL·`purpose=user_approval`) → **정식 경로** `respondHitlRequest` 승인 → `completion_met={user_approval, artifact_submitted}`(`manual` 없음) → `completed`·`session_summary` 1·인박스 1·멱등 재요청 `ignored`. **E6-04 거절**: `active` 유지·플래그 유지·결정 기록 `source=hitl`·에이전트 트리거 0. N/A 2는 S-29의 데몬 절반(§2) |
| b | **Hermes 프로파일**로 같은 시나리오 + 폴백(E8-08) + 대안 없음(E8-09) | **통과** ¹ | `30_scenario_a_hermes.sh` **57/0**(88초, 연속 두 실행 동일): Researcher=hermes lane **동시 running 3**, 합류 2(그룹당 시스템 메시지 1), Lead 기상 3(START·JOIN·JOIN), 제출 후 진행률 1/2. probe `tool_surface` hermes=**cli_wrapper** / claude_code=**mcp**, 래퍼 절대 경로 호출 3건(lane당 1). 폴백 E8-08(workdir 재사용·`runtime_kind` 변경 시 콜드 스타트·같은 머신)·E8-09(알림 1건·다른 머신으로 안 넘김) 15/15. 비용: 세션 $0.3049, `task_usage` 7행 전부 `estimated=true`(FR-7.3 추정 배지 정상 경로; hermes는 모델을 보고하지 않아 프로파일 모델로 매김) |
| c | **blocked 왕복** E3-05·06·07 + 웹 질문 카드 | **통과** | `31_blocked_roundtrip.sh` EVAL 순서 **29/0**: 카드 `blocked_q`·`lane.blocked_message_id`·위임자 즉시 기상 1(형제 상태 무관)·기상 메시지가 카드 id·본문 인용 + "카드 스레드 답글로 자식 멘션" 안내·합류가 blocked를 종료 취급하고 질문 재포함·답글 → 규칙 1 같은 lane·`reentry_count` 0→1·**`runtime.resume outcome=resumed`**. 웹 K3 배지 `질문 → @위임자`. **S-31 순서**(위임자가 즉시 답해 재진입 lane이 마지막으로 끝남) 별도 실행 **20/0**: 합류 정확히 1회, 위임자 기상 1회(재진입 통보 트리거 + 합류가 `coalesced_message_ids` — K-5·FR-3.4 모양 그대로) |
| d | **루프 상한** E4-03 | **통과** | `32_loop_limit.sh` **15/0**: 워크스페이스 설정 PATCH 200, 상한 2 → 관측 왕복 3 → `paused(loop)`·`limit=pair_roundtrips`·`count=3`·`agents` 2·넘긴 트리거 task 0·Director HITL `source=system`. 부분 갱신 뒤 형제 키 보존(S-26) |
| e | **템플릿 3분** — 템플릿에서 팀 생성 → 세션 시작 | **경로 통과 · 시간은 Director 실측 대기** | `34_template_3min.sh` **14/0**: 템플릿 3종·프로파일 매핑 9/9·에이전트 3명 일괄 생성·`definition_source` 기록·마법사 후보·세션 생성·첫 task 실행. 기계 소요(agent-browser DOM 조작 기준) 팀 생성→세션 시작 **10초**, 첫 task까지 10초. 사람 실측 절차 §6.3, 예상 2~2.5분 |

¹ 1판(#100 초판)은 45/6 — Hermes 턴이 세션에 아무것도 남기지 못했다(D-7). 원인은 협업 코어가 아니라 **데몬이 Hermes에게 도구를 건네는 자리**였다: Hermes ACP 어댑터는 `session/new.mcpServers`를 조용히 무시하고(initialize에 `mcpCapabilities` 없음) 셸 env를 위생화해 `colab`이 PATH에 없었다. 계약 v0.8/v0.8.1(`tool_surface`: `mcp`/`cli_wrapper`, attempt별 래퍼 + 브리프·턴 프롬프트의 `colab ` 치환)과 데몬 #97로 닫힌 뒤 57/0.

**판정 논리.** G5가 묻는 것은 "협업 코어가 **여러 런타임 위에서, 사람이 끼는 지점까지 포함해** 끝까지 도는가"다. 다섯 항목이 우회 없는 정식 경로로 섰고(1판의 `completeSession` 우회는 계약 #101·서버 #103으로 정식 경로가 된 뒤 다시 쟀다), 수치는 Integrator가 아닌 리뷰어가 DB에서 다시 세어 일치했다. 1판의 실패 11건은 전부 **통합에서만 드러나는 부류**(런타임이 MCP를 무시, P2 op 501, 카드 모양, 분기 하나가 두 질문을 겸함)였고 hotfix 두 라운드(#97·#103)로 닫았다. 남은 (e)의 사람 수치는 기계 하한과 예상치가 3분 안이므로 게이트를 막지 않는 것으로 판단하되, **실측은 Director 몫으로 남긴다**.

## 2. 통합이 드러낸 결함과 처리

| ID | 스트림 | 내용 | 처리 |
|---|---|---|---|
| **D-7** | D+계약 | Hermes에 colab 도구 표면이 닿지 않음 — MCP 무시 + `colab` PATH 부재. probe 13항목이 초록인데 한 마디도 못 함("광고는 초록인데 못 쓴다"는 새 부류) | **#94·#96(harness v0.8/v0.8.1)·#97** — 실기 스모크 + 30_ 57/0 |
| S-24·S-30 | S | `createAgent`가 폴백 프로파일을 조용히 버림 · `createAgentProfile`/`updateAgentProfile`(P2) 501 → E8-08 우회, 템플릿 매핑 실패 시 프로파일 없는 에이전트가 남음 | **#103** |
| S-25 | S+계약 | `user_approval`을 채울 HTTP 입구 없음(`respondHitlRequest` x-phase P3) | **계약 #101**(플랫폼 발행 approval의 승인·거절만 P2) + **#103**(0012 `hitl_request.purpose`) |
| S-26 | S | 워크스페이스 설정 부분 갱신이 형제 키를 지움(`mergeJSON`=replace) | **#103** |
| S-27·S-28 | S+계약 | `blocked_q` 카드에 위임자 멘션 없음 · 기상 메시지가 카드를 인용하지 않고 "자식 멘션" 안내 없음(규칙 4) | **#101·#103** |
| S-29 | S(+D) | 완료 시 서버가 `gc` 명령 0건 | **#103**(서버 절반: `gc {workdir_ids}` 1건). 데몬 gc 핸들러는 **D-4**(P3·P4) 범위 — 재측정 N/A 2 |
| **S-31** | S | 재진입 lane이 합류 그룹의 마지막으로 끝나면 합류가 영영 발화하지 않음(`afterLaneDone`의 한 분기가 두 질문을 겸함) — FR-6.5 조용한 손실 | **#103** — S-31 순서 20/0 |
| **S-35** | S | 재측정 관찰의 정정(#105 리뷰 NN1): 보고서 §10.3 "유휴 데몬에는 얹을 보고가 없다"는 **틀린 진단**이다 — daemon-protocol §4.1 claim long-poll 응답의 `commands[]`가 유휴 전달 경로이고 실제로 데몬이 받아 `command gc ignored (P4)`를 찍었다. 진짜 결함은 **`daemon_command.delivered_at`을 아무도 쓰지 않는 것**(프로덕션 대입 0곳) — "최소 한 번" 전달(E11-05)·재전달 판단이 이 칸에 기대면 증명할 수 없다(관측성) | **열림 — P3 첫 서버 작업.** gc 핸들러 부재는 **D-4** 그대로 |
| 기타 | — | S-21·S-22·S-23(#95 NN), S-32·S-33·S-34(#103 NN), D-8(#97 NN), S-11·S-12·S-17·S-18·S-19, W-5, D-2, C-3, K-4 | 백로그, G5 밖 |

## 3. 운영에서 배운 것 (PLAN §10.7 되먹임)

- **"광고는 초록인데 실제로는 못 쓴다."** 능력 광고는 런타임이 할 수 있는 것만 말하고 플랫폼이 그 런타임과 말할 수 있는가는 말하지 않았다. `tool_surface`가 그 칸이다. acpfake는 `mcpServers`를 존중하도록 함께 구현돼 구현과 같은 가정을 공유했다 — fake가 증명 못 하는 것을 e2e가 정확히 그 자리에서 잡았다.
- **문서가 약속한 표면을 아무도 실행해 본 적이 없었다.** 브리프 [2]는 P1부터 `colab message post`를 쓰라고 적었는데 Claude Code는 MCP만으로 충분해 `colab`이 PATH에 없다는 사실이 G3·G4를 통과했다.
- **표면이 없으면 에이전트가 API를 지어낸다.** 토큰을 쥔 에이전트가 `curl $COLAB_SERVER_URL/api/v1/...`로 없는 경로를 두드렸다. 막힘이 "아무 일도 안 일어남"으로 끝나지 않는다.
- **"둘 중 하나" 분기를 의심하라.** 재진입 통보와 합류 판정처럼 독립인 두 질문이 한 분기를 공유하면 특정 순서에서만 조용히 사라진다(S-31). EVAL이 적은 순서만 돌리면 안 잡힌다 — Integrator가 순서를 바꿔 본 것이 값을 했다.
- **"규칙이 옳은데 사람에게 그 말을 안 한다"**(S-28 규칙 4, S-30 unmapped). 프롬프트와 응답 본문은 계약의 일부다.
- **단언이 틀린 자리도 적는다**(§10.6: `jq //`가 false를 빈 값으로, psql boolean 표기, 병합되면 `coalesced_message_ids`). 마지막 것은 FR-3.4가 제대로 도는 것을 미달로 오보할 뻔했다.
- **Orca 운영**: 결함 ID는 백로그가 SSOT(보고서 번호 충돌 → 재번호), worker_done 뒤 재작업은 터미널 send + 브랜치 푸시 감시, 계약 `x-phase`는 `P2 + 주석` 관례(문자열 값이면 web 생성 타입 CI가 깨진다), 스펙에 PR 번호가 빈 채 task를 만들면 지울 수 없다(`task-update failed`로만 표기).

## 4. 확인 요청

Director가 할 것은 하나다 — **템플릿 3분 실측**(`G5_REPORT.md §6.3`: `/agents` → [팀 템플릿] → 리서치 팀 → 새 세션 마법사 7단계 → S7 첫 카드; 전제는 페어링·probe 완료). 3분 안이면 G5는 조건 없이 확정이고, 넘기면 넘긴 단계의 화면을 P3 안에서 고친다. 판정 근거는 PR #100·#105와 `e2e/p2/out/`에 있고 `bash e2e/p2/up.sh && bash e2e/p2/33_approval_completed.sh`(약 60초)로 재현된다.

## 5. 확인 후 순서

1. **P3 계획(G6: 시나리오 C·D + 중복 0 = 컷 2·3 판정)** — HITL 전반(`respondHitlRequest` 나머지, `listInbox`, deputy), 재개, 예산 강제. P3 첫 서버 작업에 **S-35**(`delivered_at` 기록)를 얹고, **D-4**(데몬 gc 핸들러) 시점을 P3 계획에서 정한다.
2. **Reviewer의 colab Hermes 프로파일 전환**(PLAN §10.1 "G5 후") — `tool_surface=cli_wrapper`가 실기로 검증됐으므로 가능. Orca 리뷰 흐름과 병행 여부는 P3 첫 작업에서 정한다.
3. 백로그 처리 시점: S-21(Hermes 모델 단가 — 프로파일 모델로 매기므로 실해 없음, 배지 문구만), S-32~S-34, D-8은 P3 첫 서버·데몬 작업에 얹는다.
