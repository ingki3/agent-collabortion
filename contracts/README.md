# contracts — 스트림 간 계약

네 스트림(server · daemon · cli · web)의 에이전트가 서로를 알지 못한 채 병렬로 가기 위한 **유일한 공유 지점**이다. PLAN.md §1 원칙 5 "인터페이스 계약이 곧 에이전트 간 대화다."

## 여기에 들어오는 것 (P0-b, G2에서 확정)

| 파일 | 내용 | 소유 |
|---|---|---|
| `openapi.yaml` | 서버 REST API | S + W |
| `task_event.schema.json` | `task_event` — class · verb · object_ref · outcome | S + D + W |
| `daemon-protocol.md` | 데몬↔서버: claim · heartbeat · event push · 토큰 폐기 통보(서버 → 데몬) · 큐 인터페이스 | S + D |
| `harness.md` | 하네스 인터페이스: `transport: acp \| cli`, 권한 응답 정책, 툴 차단, 브리프 전달 경로 — **스파이크 1·2·3 판정(G1) 뒤에만** | D + Lead |
| `colab-cli.md` | `colab` CLI 명령 표(PRD FR-7.4) + `COLAB_TASK_TOKEN` 형식 + "턴을 끝내라" 반환 규약 | C + D |
| `clock/` | 주입 가능한 클럭 인터페이스 — 시간 의존 로직(유예·기한·만료) 전부 이것을 경유 | S + D |
| Go 패키지 | 위 계약의 Go 타입(프로토콜 메시지, 이벤트). server·daemon·cli가 import | — |

## 변경 규칙 (PLAN.md §10.3)

- **구현 PR은 이 디렉토리를 건드리지 못한다.** 계약을 바꿔야 하면 `blocked`로 Lead에게, Lead가 Director에게.
- 계약 변경은 **Director 승인 PR**로만 들어온다. `.github/CODEOWNERS`가 이 디렉토리를 Director에게 묶는다.
- G2 전에는 비어 있다. 스파이크 결과 없이 계약을 고정하지 않는다(PLAN.md §1 원칙 3).
