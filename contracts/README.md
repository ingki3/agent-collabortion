# contracts — 스트림 간 계약

네 스트림(server · daemon · cli · web)의 에이전트가 서로를 알지 못한 채 병렬로 가기 위한 **유일한 공유 지점**이다. PLAN.md §1 원칙 5 "인터페이스 계약이 곧 에이전트 간 대화다."

## 여기에 들어오는 것 (P0-b, G2에서 확정)

| 파일 | 내용 | 소유 | 상태 (v0.1 G2 draft) |
|---|---|---|---|
| `harness.md` | 하네스 인터페이스: 런타임 바인딩·`_meta`·권한 응답·취소·재개·이벤트 정규화·오류 분류·능력 광고 — G1 판정과 스파이크 실측 기반 | D + Lead | ✅ 초안. §12 미결은 스파이크 1b가 닫는다 |
| `daemon-protocol.md` | 데몬↔서버: 페어링·probe·claim(long-poll)·events·heartbeat·명령(cancel/revoke/probe/gc/rebind)·finish·토큰 폐기(서버 → 데몬)·workdir/GC·큐 인터페이스 | S + D | ✅ 초안 |
| `task_event.schema.json` | `task_event` — class · verb · object_ref · outcome + payload $defs + 렌더 클래스 파생 규칙 | S + D + W | ✅ 초안 |
| `colab-cli.md` | `colab` CLI/MCP 명령 표(FR-7.4) + 토큰 + 종료 코드 + "턴을 끝내라"(`turn_end_required`) | C + D | ✅ 초안 |
| `clock/` | 주입 가능한 클럭 — `Clock` 인터페이스, `Real`, `Fake`(Advance/Set) | S + D | ✅ |
| `protocol.go` | 위 계약의 Go 타입·열거값·타이밍 상수. server·daemon·cli가 import | — | ✅ |
| `openapi.yaml` | 서버 REST API (사람 화면 + colab CLI가 쓰는 엔드포인트, 데몬 API 제외) | S + W | 🔄 S worker 작성 중 |

## 변경 규칙 (PLAN.md §10.3)

- **구현 PR은 이 디렉토리를 건드리지 못한다.** 계약을 바꿔야 하면 `blocked`로 Lead에게, Lead가 Director에게.
- 계약 변경은 **Director 승인 PR**로만 들어온다. `.github/CODEOWNERS`가 이 디렉토리를 Director에게 묶는다.
- G2 전에는 비어 있다. 스파이크 결과 없이 계약을 고정하지 않는다(PLAN.md §1 원칙 3).
