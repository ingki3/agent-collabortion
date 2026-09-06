# 스파이크 4c — 콜드 스타트 정성 평가: 브리프 + 히스토리 + 결정 기록만으로 작업을 이어가는가

| 항목 | 내용 |
|---|---|
| 근거 | PLAN §4 4c(P3 착수 전 게이트), `plan/P3_TASKS.md` T-D4. PRD §8.4(턴 프롬프트 `<resumed>`·"이미 게시한 메시지 목록"·"workdir 상태 확인")·FR-5.4·FR-7.1, `contracts/harness.md` §6(재개·`resume_rejected` → 콜드 스타트)·§2.2(턴 종료 판정), EVAL E8-02·E8-03·E8-04·E8-12 |
| 실행일 | 2026-09-06 KST 20:53~21:52 (실기 배치 5회, 유효 런 **35회**(a 5 · b 15 · c 5 · e 5 · f 5) + 스모크 3회. 모델 haiku, 한도 미도달) |
| 런타임 | Claude Code CLI 2.1.258 + 어댑터 `@agentclientprotocol/claude-agent-acp` **0.74.0** · **Hermes 0.20.6**. 둘 다 `none` 격리, 실제 로그인 |
| 스택 | 전용 Postgres `colab-pg-s4c`(:5441) + server(:8095), 웹 없음. 다른 워커 스택과 포트·컨테이너·workdir 분리(P3_TASKS §0-13). 종료는 pid·pgid 만(§0-10) |
| 과제 | 저장소 밖 무해한 과제(X-2): 가상의 스마트 물병 카탈로그 조각 `part-one.md`·`part-two.md` 를 각 6항목까지 채우고 단계마다 `colab message post`. 저장소 파일명·시나리오 이름을 goal 에 쓰지 않았다 |
| 도구 | `plan/spikes/spike04c/` — `up.sh`·`run_batch.sh`(배치 러너)·`measure.py`(판정기)·`hermes_forget.py`·`hermes_wire.py`. claim 탭은 `e2e/p2/fixtures/claimtap.py` 재사용(턴 프롬프트는 디스크에 남지 않는다) |
| 원시 로그 | `plan/spikes/logs/spike4c_runs_b{1..5}.jsonl`(런별 판정치 + 턴 프롬프트 전문), `plan/spikes/logs/spike4c_hermes_wire.json`(Hermes `session/load` 원시 응답) |
| 분리한 코드 PR | **#111**(머지됨, dev `edfc380`) · **#115**(제출) — 아래 §1·§2. 스파이크 보고서에는 근거만 두고 diff 는 별도 브랜치로 뺐다(T-D4 지시) |

---

## 총평

**판정: 통과 — 단, 데몬 수정 2건이 선행돼야 성립한다.**

콜드 스타트한 에이전트는 **작업을 이어간다**. 지금의 브리프 + 히스토리 + `<resumed>` 구간만으로 20/20(콜드 스타트 4갈래 × 5회)이 남은 작업을 끝냈고, **같은 메시지 재게시 0 · 같은 편집 중복 적용 0 · "workdir 먼저 확인" 준수 20/20** 이다. 히스토리 창에서 **자기 발화가 한 줄도 남지 않은** 극단(§4 (f))에서도 결과는 같았다. **PLAN 이 예비해 둔 폴백("직전 활동 피드 요약 주입" = §8.4 계약 변경 PR)은 지금 발동할 근거가 없다.**

이어가게 만든 것은 히스토리가 아니라 **workdir 이다.** 프롬프트의 "이미 게시한 메시지" 목록은 **UUID 뿐**이라 그 자체로는 무엇을 게시했는지 알려주지 않는다(§5-1). 20/20 이 성립한 이유는 이 과제의 상태가 전부 파일에 있었기 때문이다 — 그 조건이 깨지는 작업 유형에 대한 권고를 §5 에 적었다.

**그러나 그 판정에 도달하기까지, 유실 감지가 두 런타임 모두에서 실기에 한 번도 발동한 적이 없다는 것이 드러났다.** 계약 `harness.md` §6 의 두 판정 규칙이 모두 실제 런타임의 응답과 어긋나 있었고, 유닛·계약 테스트는 페이크가 **계약 문구 그대로** 답했기 때문에 전부 초록이었다. 하나는 attempt 를 태워 task 를 죽이고(§1), 다른 하나는 **작업을 조용히 잃는다**(§2). 두 건 다 Lead 판정을 받아 별도 PR 로 분리했다.

---

## 0. 방법

### 0.1 한 런의 모양

```
warm-up 턴  : 세션 시작 → 에이전트가 계획 한 줄만 게시하고 턴 종료
              → finish 가 lane.runtime_session_ref 를 심는다        ← (a) 가 성립하는 유일한 경로
본 작업     : Director "시작" → attempt 1 → 파일 절반 편집 + 메시지 2~3개 게시
kill        : 데몬을 **SIGKILL** (pgid)                              ← SIGTERM 은 안 된다(§0.2)
유실 유도   : arm 별로 런타임 쪽 세션 기록을 지운다
재큐잉      : 서버 heartbeat 3분 만료 → running → queued, attempt 2 (E5-03)
attempt 2   : 데몬 재기동 → 서버가 만든 `<resumed>` 프롬프트로 재개/콜드 스타트
판정        : 파일 실물 + DB(message·task_event·task_attempt) + claim 탭의 턴 프롬프트
```

한 배치에 세션 5~10개를 같은 데몬에 얹고 **한 번만** kill 한다 — 3분 만료 창을 런들이 공유한다.

### 0.2 두 가지 함정 (재현하려는 사람을 위해)

- **kill 은 SIGKILL 이어야 한다.** SIGTERM 은 데몬의 정상 종료 경로라 running task 를 `finish(outcome=cancelled)` 로 닫아 버린다. 실측: 스모크 1회가 `status=cancelled`, attempt 1 에서 멈췄다. 크래시를 재현하려면 finish 가 **아예 가지 않아야** 재큐잉이 일어난다.
- **`lane.runtime_session_ref` 는 `finish` 에서만 저장된다**(`server/internal/tasks/service.go:511`). 데몬이 턴 도중 죽으면 그 attempt 의 ref 는 서버에 남지 않는다. 그래서 **"데몬이 죽었다 → 다음 attempt 가 resume 한다"는 경로는 그 task 만으로는 성립하지 않는다** — 같은 lane 의 **이전 task 가 정상 종료하며 심어 둔 ref** 가 있어야 한다. 위 warm-up 턴이 그것이다. E8-04(중단 → 재큐잉)를 warm-up 없이 그대로 돌리면 `resume` 필드가 아예 비어 콜드 스타트가 된다. **이것이 실사용의 기본 모양이다** — 권고 §5-5.

### 0.3 판정 항목

| 항목 | 정의 | 측정 |
|---|---|---|
| 이어갔는가 (`continued`) | 두 파일이 각각 항목 1~6 을 갖고 `ALL-DONE` 이 게시됐는가 | workdir 실물 + `message` |
| 같은 파일을 이어서 | attempt 1 이 만든 파일을 그대로 편집했는가(새 파일 0) | kill 직후 스냅샷 ↔ 최종 |
| 재게시 0 (`dup_messages`) | kill 전에 이미 게시한 STAGE 라벨이 attempt 2 에서 다시 나온 수 | `message.source_task_id` |
| 중복 편집 0 (`dup_edits`) | 파일 안에 같은 항목 번호가 두 번 이상 | 항목 번호 파싱 |
| workdir 먼저 확인 | attempt 2 의 **첫 확인/편집 툴**이 `read`·`run_shell`·`search` 인가(`edit_file` 이면 위반) | `task_event(class=tool)` |

---

## 1. 결함 ① — claude_code 유실 신호가 계약과 다르다 (E8-02 가 실기에서 한 번도 발동한 적 없다)

계약 §6 은 claude_code 유실 신호를 JSON-RPC 오류 **`"Session not found"`** 로 적는다. 그 문자열의 출처는 `SPIKE_04a.md` §2 인데, 거기 적힌 대로 그것은 **어댑터 dist 소스 독해**(`acp-agent.js:171`)였고 실측이 아니었다.

**실측 (어댑터 0.74.0 + CLI 2.1.258):**

```
session/load: rpc error -32002: Resource not found: <sessionId>  data={"uri":"<sessionId>"}
```

존재한 적 없는 id 든, `~/.claude/projects/<cwd 인코딩>/<sessionId>.jsonl` 을 **실제로 지운** 진짜 유실이든 **같은 오류**다(10/10).

데몬(`runner.go` `load()`)은 소문자 `"session not found"` 부분일치만 봤으므로 이 오류를 유실로 읽지 못하고 attempt 를 `failure_kind=other` 로 실패시켰다.

| attempt | outcome | failure_kind |
|---|---|---|
| 1 | runtime_offline (SIGKILL) | runtime_offline |
| 2 | other | other |
| 3 | other | other |
| → task | **failed** | max_attempts 3 소진 |

즉 **E8-02(`resume_rejected` → 콜드 스타트)는 실기에서 한 번도 발동한 적이 없다.** 유닛·계약 테스트가 전부 초록이었던 이유는 `acpfake` 가 계약 문구 그대로 `-32000 "Session not found"` 를 돌려줬기 때문이다.

**Lead 판정(질의 1): "(1) 실측이 계약을 이긴다."** 계약 §6 정정(v0.8.2)은 Lead 가 별도 PR 로, 데몬 수정은 **PR #111** (`fix/daemon-resume-rejected`, 머지됨 dev `edfc380`) — 판정을 `code == -32002 || 메시지에 "not found"(대소문자 무시)` 로 넓히고 `acpfake` 의 **기본값을 실기 모양으로** 바꿨다. 회귀 주입으로 실패 모습이 실기 증상과 같음을 확인했다.

이 스파이크의 (c)·(e)·(f) 표는 **PR #111 빌드**로 잰 것이다.

## 2. 결함 ② — Hermes 는 provenance 없이 답하고, 데몬은 그것을 `resumed` 로 읽는다 (작업이 조용히 사라진다)

데몬을 거치지 않고 `hermes acp` 와 직접 stdio JSON-RPC 로 잰 원시 와이어(`plan/spikes/logs/spike4c_hermes_wire.json`):

| 단계 | `session/load` 응답 |
|---|---|
| 대조군 — 세션 살아 있음 | `result { _meta.hermes.sessionProvenance { acpSessionId=<요청 id>, rootHermesSessionId=…, sessionKind:"root", compressionDepth:0 }, models… }` |
| SPIKE_04a §(b) 와 **같은 방법**으로 `~/.hermes/state.db` 의 그 세션 행 삭제 후 | **`result {}`** — `null` 이 아니고 `_meta` 도 없다 |
| 그 뒤 `session/prompt` | **`{"stopReason": "refusal"}`** — 아무것도 하지 않고 턴이 끝난다 |

계약 §6 hermes 행은 (a) 결과 `null` (b) provenance 불일치 두 경우만 적는다. **결과는 있는데 provenance 가 없는 세 번째 모양이 계약에 없고**, 데몬(`runner.go:548`)은 그 경우 `return ref.SessionID, …` 즉 **resumed** 로 처리했다. 그리고 `stopReason: refusal` 은 §2.2 가 "정상 종료"로 못박아 둔 값이다(유실 판정에 쓰지 않는다 — G1 F7). 체인이 이렇게 닫힌다:

```
유실 감지 실패 → resumed 로 보고 → prompt refusal → 턴 정상 종료 → attempt completed
```

**수정 전 실측(§4 (b) 1차, 5회):** `runtime.resume outcome=resumed` **5/5**(유실인데), attempt 2 의 파일 편집 **0**, 남은 STAGE 게시 5회 중 3회 0건·2회 1건, 작업 완수 **0/5**, task 최종 상태 **completed 5/5**. 파일은 절반에서 멈춘 채 아무도 실패했다고 말하지 않는다.

**Lead 판정(질의 2): "(1) fail-closed."** 계약 §6 (a′) 추가(v0.8.3)는 Lead 가, 데몬 수정은 **PR #115** (`fix/daemon-hermes-provenance-missing`) — provenance 부재(빈 객체 포함)를 `resume_rejected`(reason `no_provenance`) → 콜드 스타트로. 콜드 스타트는 느릴 뿐이지만 거짓 resumed 는 작업을 잃는다.

> **유도 방법 대조 (Lead 요청).** 예비 회차는 `lane.runtime_session_ref.session_id` 를 존재한 적 없는 UUID 로 바꾸는 값싼 방식이었다. E8-03 의 정식 유도가 아니므로 1·2차는 SPIKE_04a §(b) 와 **같은 방법** — `~/.hermes/state.db` 의 그 세션 행 삭제 — 으로 다시 쟀다(사용자의 다른 세션은 건드리지 않는다: `hermes_forget.py` 가 `source='acp'` 인 그 id 한 행과 딸린 `messages` 만 지운다). **두 유도가 같은 결과를 냈고**, 계약 §6 이 적은 `null` 응답은 **두 유도 어느 쪽에서도 한 번도 나오지 않았다** — 실기 응답은 언제나 `{}` 였다.

## 3. 결함 ③ (권고만, 고치지 않음) — refusal 로 끝난 빈 턴이 `completed` 가 된다

§2 의 유실 감지를 고쳐도 남는 별개 문제다. `session/prompt` 가 `stopReason: refusal` 로 돌아오면 §2.2 는 정상 종료로 읽고, 편집 0·게시 0 이어도 attempt 는 `completed` 가 된다. Lead 가 T-D5 스펙으로 가져가기로 했고, 권고는 둘이다.

1. `refusal` + 그 attempt 의 편집·게시가 **둘 다 0** 이면 `outcome=failed(failure_kind=other)` 후보로 본다.
2. 또는 **resume 직후 첫 턴이 refusal** 이면 콜드 스타트로 **1회 재시도**한다(런타임이 세션을 잃었다는 두 번째 신호로 취급).

---

## 4. 판정 표

`n=5` 씩. `데몬` 칸은 그 배치를 돌린 빌드다.

### (a) resume 성공 — `runtime_session_ref` 있음 · claude_code (E8-01)

데몬 dev `2afce04` · 로그 `spike4c_runs_b1.jsonl`

| 런 | kill 시점 상태 | attempt 2 판정 | 이어갔나 | 같은 파일 | 재게시 | 중복 편집 | workdir 먼저 |
|---|---|---|---|---|---|---|---|
| Wa1 | A1·B1 게시 / 6·3항목 | `resumed` | ✅ | ✅ | 0 | 0 | ✅ |
| Wa2 | A1·B1·A2 게시 / 6·3항목 | `resumed` | ✅ | ✅ | 0 | 0 | ✅ |
| Wa3 | A1·B1 게시 / 6·3항목 | `resumed` | ✅ | ✅ | 0 | 0 | ✅ |
| Wa4 | A1·B1 게시 / 6·3항목 | `resumed` | ✅ | ✅ | 0 | 0 | ✅ |
| Wa5 | A1·B1 게시 / 3·3항목 | `resumed` | ✅ | ✅ | 0 | 0 | ✅ |
| **합** | | **resumed 5/5** | **5/5** | **5/5** | **0** | **0** | **5/5** |

### (b) 강제 콜드 스타트 — Hermes, `~/.hermes/state.db` 세션 행 삭제 (E8-03)

| 차수 | 유실 유도 | 데몬 | 유실 판정 | 이어갔나 | 재게시 | 중복 편집 | workdir 먼저 | 로그 |
|---|---|---|---|---|---|---|---|---|
| 예비 | lane ref 의 `session_id` 를 없는 UUID 로 | dev `2afce04` | **`resumed` 5/5 — 오판** | **0/5** | 0 | 0 | 0/5 | `b1.jsonl` |
| 1차 | **`state.db` 세션 행 삭제**(E8-03 정식) | PR #111 빌드(hermes 경로는 dev 와 동일) | **`resumed` 5/5 — 오판** | **0/5** | 0 | 0 | 0/5 | `b2.jsonl` |
| 2차 | 같음 | **PR #115 빌드** | **`cold_start`/`no_provenance` 5/5** | **5/5** | **0** | **0** | **5/5** | `b3.jsonl` |

2차 런별(전부 동일): Wb1~Wb5 — kill 시점 3~6항목/3항목, attempt 2 가 `pwd && ls -la`·`git status` → 두 파일 `read` → 남은 항목 편집 → `STAGE-A2`/`STAGE-B2`/`ALL-DONE` 게시.

1차는 §2 의 결함 그 자체다 — attempt 2 가 `runtime.resume outcome=resumed` 를 내고 **툴 이벤트 0개**로 턴을 끝냈다.

### (c) 강제 콜드 스타트 — claude_code, transcript 파일 삭제 (E8-02)

데몬 PR #111 빌드 · 로그 `b2.jsonl`. **(a) 와 런타임·과제·프롬프트가 같고 갈린 것은 "런타임이 이전 턴을 기억하는가" 하나뿐인 대조군이다.**

| 런 | kill 시점 | attempt 2 판정 | 이어갔나 | 같은 파일 | 재게시 | 중복 편집 | workdir 먼저 |
|---|---|---|---|---|---|---|---|
| Wc1 | A1·B1 / 3·3 | `cold_start`/`session_not_found` | ✅ | ✅ | 0 | 0 | ✅ |
| Wc2 | A1·B1 / 3·3 | `cold_start`/`session_not_found` | ✅ | ✅ | 0 | 0 | ✅ |
| Wc3 | A1·B1 / 6·3 | `cold_start`/`session_not_found` | ✅ | ✅ | 0 | 0 | ✅ |
| Wc4 | A1·B1 / 3·3 | `cold_start`/`session_not_found` | ✅ | ✅ | 0 | 0 | ✅ |
| Wc5 | A1·B1 / 3·3 | `cold_start`/`session_not_found` | ✅ | ✅ | 0 | 0 | ✅ |
| **합** | | **cold_start 5/5** | **5/5** | **5/5** | **0** | **0** | **5/5** |

Wc1 의 attempt 2 첫 툴 순서: `Terminal` → `Read File` → `git` → `part-one.md` read → `ls`. 지시대로 **먼저 봤다.**

### (e)·(f) 보조 — 히스토리 상한이 물릴 때 (E8-12)

DoD 밖이지만 §5 의 "부족 항목"을 추측이 아니라 실측으로 적기 위해 돌렸다. (c) 와 같되 재큐잉 전에 세션 메시지를 40건 채워 히스토리 창(서버 `historyLimit = 30`)을 넘겼다. 필러는 라우터를 거치지 않고 `message` 테이블에 직접 넣었다(필러가 task 를 만들면 실험이 달라진다).

| arm | 히스토리 | 창 안에 남은 자기 발화 | 이어갔나 | 재게시 | 중복 편집 | workdir 먼저 | 로그 |
|---|---|---|---|---|---|---|---|
| (e) | `included=30 total=45 truncated=true` | STAGE-A1·B1 남음 | **5/5** | 0 | 0 | 5/5 | `b4.jsonl` |
| (f) | `included=30 total=45 truncated=true` | **0줄 — 자기 발화가 창에 하나도 없다** | **5/5** | **0** | **0** | **5/5** | `b5.jsonl` |

(f) 는 이 스파이크의 가장 강한 결과다. 에이전트는 **자기가 무엇을 게시했는지 프롬프트에서 알 수 없고**(히스토리에 자기 줄이 없고, "이미 게시한 메시지"는 UUID 뿐이다) 그런데도 재게시 0·중복 편집 0 으로 끝냈다 — **파일을 먼저 읽고 남은 일을 추론했기 때문이다.**

---

## 5. 콜드 스타트 프롬프트에 무엇이 부족한가 (정성)

프롬프트는 (a) 와 (c) 가 **글자 그대로 같다** — 서버는 재개인지 콜드 스타트인지 모르고 프롬프트를 만든다(계약대로다: "재개 턴 프롬프트는 서버가 만든다"). 실측 전문:

```
<resumed attempt=2>
Your previous attempt (1) was interrupted: runtime_offline.
Messages you already posted: f3925a70-…, 807fa09b-…, c5b39f3b-…. Do not post them again.
Before continuing, inspect the current state of the workdir (changed files, git status) and continue from there.
</resumed>

<history included=6 total=6 truncated=false>
[12:14] system: Session started. Goal: 가상의 스마트 물병 제품 카탈로그 초안 두 조각을 …
[12:14] Wa1: part-one.md와 part-two.md를 각각 여섯 줄씩 작성할 계획 (네 단계로 진행)
[12:14] Director: [@Wa1](mention://agent/…) 시작
[12:14] Wa1: STAGE-A1 done
[12:14] Wa1: STAGE-B1 done
[12:14] Wa1: STAGE-A2 done
</history>

<trigger>
<message id="59b3d450-…" author="Director" at="2026-09-06T12:14:34Z">
[@Wa1](mention://agent/…) 시작
</message>
</trigger>

Respond to the trigger. Post your reply with `colab message post`; …
```

브리프(1755자)는 `[1] Identity` `[2] Workspace rules` `[4] Session` `[5] Roster` `[8] Precedence` 다.

부족한 것을 **영향 순으로** 적는다. 번호는 결함 번호가 아니다 — 새 결함은 Lead 가 번호를 준다(P3_TASKS §0-11).

### 5-1. "이미 게시한 메시지"가 UUID 뿐이다 — **가장 큰 구멍** (신규, 스트림 S)

`Messages you already posted: f3925a70-…, 807fa09b-…` 는 에이전트가 **대조할 수 없는 값**이다. 히스토리 줄에는 메시지 id 가 붙지 않으므로 UUID ↔ 내용 매핑이 프롬프트 안에 없다. 지금 재게시 0 이 나오는 이유는 그 목록 때문이 아니라 **히스토리에 내용이 같이 있거나(a·c·e), 파일을 읽어 추론했기(f)** 때문이다.

- 값싼 수리: 목록을 `id — 앞 N자` 로 바꾸거나, 히스토리 줄에 id 를 붙여 대조 가능하게 한다. `posted_message_ids` 는 이미 TaskBundle 필드로 데몬까지 간다(§4.1) — 렌더만 바꾸면 된다.
- 이것을 고치지 않으면 §5-2 와 겹쳐 **파일에 상태가 남지 않는 작업**(조사 요약을 메시지로만 게시하는 Researcher lane 같은 것)에서 재게시가 난다. 이 스파이크의 과제는 상태가 전부 파일에 있어 그 위험이 드러나지 않았다 — **일반화하지 말 것.**

### 5-2. 브리프에 `[7] Decision Log` 와 `[6] Context` 가 아예 없다 (신규, 스트림 S)

PRD §8.4 는 브리프를 `[1]~[8]` 로 정하고, `contracts/colab-cli.md` 의 `colab decision record` 행은 **"결정 기록(source=agent). 브리프 [7]에 실린다"** 고 적는다. 실측한 브리프에는 `[3]`·`[6]`·`[7]` 이 없다(`server/internal/queue/bundle.go` 주석: "[3] coordination protocol and [6]/[7] are P2" — 그런데 P2 에서 만들어지지 않았다). `decision` 테이블과 기록 op 는 존재하는데 **읽는 쪽이 없다.**

PLAN 이 이 스파이크에 붙인 이름이 "브리프 + 히스토리 + **결정 기록**"인데 결정 기록은 지금 프롬프트에 실리지 않는다. 이 스파이크의 과제는 결정이 없어 판정에 영향이 없었지만, **시나리오 C(Director 중간 개입)에서 결정이 콜드 스타트를 넘어 살아남는지는 아직 아무도 재지 않았다.** P3 의 HITL 재개(E7·E8-01 "HITL 답변과 승인 여부")도 같은 자리에 실려야 한다 — §8.4 가 `<resumed>` 에 담으라고 적은 것 중 **"HITL 답변과 승인 여부"만 지금 빠져 있다.**

### 5-3. 직전 attempt 가 **무엇을 했는지**가 프롬프트에 없다 (PLAN 의 폴백 항목)

`<resumed>` 가 알려주는 것은 (i) 중단 사유 한 단어(`runtime_offline`)와 (ii) 게시한 메시지 id 목록뿐이다. **파일을 몇 개 고쳤는지, 셸을 무엇을 돌렸는지는 없다.** 데몬은 그것을 `task_event` 로 이미 전부 올려 두었는데(attempt 1 의 `tool/edit_file`·`run_shell`) 프롬프트는 그 자료를 쓰지 않는다.

**그런데 이번 실측으로는 그것을 넣을 근거가 없다.** "workdir 을 먼저 확인하라" 한 줄이 20/20 지켜졌고, 확인 결과가 요약보다 정확하다(요약은 프롬프트 생성 시점의 스냅샷이고 workdir 은 현재다). 따라서:

> **권고: PLAN 의 폴백("직전 활동 피드 요약 주입" = §8.4 계약 변경 PR)은 지금 발동하지 않는다.** 발동 조건을 대신 남긴다 — **격리가 `none`/`worktree` 가 아니어서 workdir 을 다시 읽을 수 없거나, lane 의 산출물이 파일이 아니라 메시지·아티팩트뿐인 작업 유형에서 재게시/중복이 관측되면** 그때 넣는다. §5-1 이 먼저다(더 싸고, 같은 구멍을 막는다).

### 5-4. 히스토리 상한이 30 이다 — EVAL E8-12 는 50 을 쓴다 (신규, 스트림 S · 사소)

`server/internal/queue/bundle.go`의 `historyLimit = 30`. E8-12 의 케이스는 "히스토리 200개, 프롬프트 상한 50개"다. `included/total/truncated` 표기는 계약대로 정확히 나온다(실측 `included=30 total=45 truncated=true`). 상한 값 자체는 설정 가능해야 하는지, 30 이 맞는지는 Lead 판정 사항이다. 창은 **최신 30개**를 시간순으로 렌더한다(실측) — 계약 §8.4 "최근 히스토리 N개"와 맞다.

### 5-5. `runtime_session_ref` 는 `finish` 에서만 저장된다 (신규, 스트림 S/D · 설계 판단 필요)

§0.2 참조. 데몬이 턴 도중 죽으면 그 attempt 의 세션 ref 가 서버에 없다. 즉 **resume 이 가장 필요한 상황(크래시)에서 resume 자원이 없다.** 실사용에서 lane 의 첫 task 가 크래시하면 attempt 2 는 항상 콜드 스타트다.

이번 실측 결과로는 **그게 문제가 아니다** — 콜드 스타트가 resume 과 같은 성적을 냈다(20/20 vs 5/5). 그러니 "고쳐라"가 아니라 **"알고 있으라"** 로 남긴다. 굳이 줄이려면 세션 생성 직후 ref 를 heartbeat 에 실어 올리는 것이 방법이지만, 그것은 §4.2 계약 변경이고 얻는 것이 작다.

### 5-6. 사소한 것들

- 턴 프롬프트가 영어고 브리프·에이전트·세션은 한국어다. 판정에 영향은 없었다.
- `Your previous attempt (1) was interrupted: runtime_offline.` — `failure_kind` 원문이 그대로 나간다. 사람이 읽는 문장은 아니지만 에이전트에게는 충분했다.
- `git status` 를 git 저장소가 아닌 workdir 에서 돌린 런이 여럿 있었다(프롬프트가 그렇게 시킨다). 무해하지만 `none` 격리에서는 문구를 나눌 여지가 있다.

---

## 6. 한 줄 결론

> **통과.** 콜드 스타트한 에이전트는 브리프 + 히스토리 + `<resumed>` 만으로 작업을 이어간다(콜드 스타트 20/20, 재게시 0, 중복 편집 0, "workdir 먼저 확인" 20/20). **§8.4 계약 변경(직전 활동 피드 요약 주입)은 필요하지 않다.** 다만 P3 착수 전에 데몬 수정 2건(**PR #111 머지됨 · PR #115 제출**)과 계약 §6 정정 2건(Lead, v0.8.2·v0.8.3)이 필요하고, 브리프 `[7] 결정 기록` 부재(§5-2)와 "이미 게시한 메시지"가 UUID 뿐인 문제(§5-1)는 **시나리오 C 를 재기 전에** 서버 쪽에서 닫아야 한다.

## 7. Lead 결정 사항 (이 스파이크에서 받은 것)

| 질의 | 결정 | 반영 |
|---|---|---|
| §1 claude_code 유실 신호가 계약과 다르다 | **(1) 실측이 계약을 이긴다.** 계약 §6 → "코드 -32002 또는 메시지에 not found → resume_rejected"(v0.8.2, Lead PR). 데몬은 별도 PR | PR #111 (머지) |
| §2 Hermes provenance 부재 판정 | **(1) fail-closed.** 계약 §6 hermes 행에 (a′) 추가(v0.8.3, Lead PR). 데몬은 새 브랜치·새 PR | PR #115 |
| §3 refusal 빈 턴이 completed | 스파이크 범위 밖 — 권고만 남기고 Lead 가 **T-D5 스펙**에 넣는다 | §3 |

## 8. 재현

```bash
bash plan/spikes/spike04c/up.sh                      # 전용 PG(:5441) + server(:8095)
TAP_PORT=8096 bash plan/spikes/spike04c/run_batch.sh myrun a:claude_code:5 b:hermes:5
#   arm a=resume 성공 · b=hermes 유실 · c=claude_code transcript 삭제
#   e/f=c + 히스토리 필러(FILLER_GAP_MS 로 필러 간격을 좁히면 자기 발화가 창 밖으로 나간다)
python3 plan/spikes/spike04c/hermes_wire.py          # Hermes session/load 원시 응답
bash plan/spikes/spike04c/down.sh                    # pid 파일만으로 종료
```

한 배치는 kill → 3분 heartbeat 만료 → 재기동까지 약 12분이다. 결과는 `plan/spikes/spike04c/out/<batch>.jsonl` 한 줄에 런 하나.
