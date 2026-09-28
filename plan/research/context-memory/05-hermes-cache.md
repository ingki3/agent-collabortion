# 05 — hermes 캐시 조사 (T-HERMESCACHE, 1단계 ④)

작성 2026-09-28 · 정본 `../CONTEXT_MEMORY.md` · 선행 `04-baseline.md`(0단계 기준선 — PR #382, 아직 dev 밖)
계기: 0단계 기준선에서 Designer·Developer(hermes, `anthropic:claude-opus-5-5`)가 방 비용의 56%를 쓰고, 비캐시 input 이 1.3억~1.5억인데 cache_write 보고가 0이었다.

---

## 0. 결론 먼저

**hermes 는 prompt caching 을 쓰고 있었고, 아주 잘 듣고 있었다. 틀린 것은 우리 데몬의 읽는 법이다.**

| # | 물음 | 답 |
|---|---|---|
| (1) | hermes 가 Anthropic prompt caching(cache_control)을 쓰는가 | **쓴다.** `agent/prompt_caching.py` 가 breakpoint 4개(정적 시스템 접두 · 시스템 끝 · 마지막 비시스템 메시지 2개)를 붙이고, `~/.hermes/config.yaml` 의 `prompt_caching: {cache_ttl: 5m, long_lived_prefix: true, long_lived_ttl: 1h}` 가 켜져 있다. **실측**(`hermes acp` 2턴): 2턴째 `cachedReadTokens = 26,244` — 1턴째 프롬프트를 통째로 캐시에서 읽었다. 게임 제작 방 실사용도 캐시 적중률 중앙 **91.6%**(프롬프트 2.78억 토큰 중 2.38억이 캐시 읽기) |
| (2) | 우리 데몬의 hermes 경로가 캐시 접두를 매번 깨는가 | **아니다.** `COLAB_BRIEF.md` 는 파일이라 프롬프트 접두에 들어가지 않고(에이전트가 읽어야 보인다), 턴 프롬프트는 대화의 **꼬리**로 붙는다. claude_code 에서 [6]·[7] 이 시스템 프롬프트를 바꿔 접두를 깨던 문제(04 §3)가 hermes 에는 **없다** — 적중률 91.6% 가 그 증거다 |
| (3) | usage 보고가 cache 칸을 버리는가 | **버리는 것보다 나쁘다 — 같은 토큰을 두 번 청구했다.** hermes 의 `inputTokens` 는 캐시 토큰을 **포함**하는데 데몬이 우리 계약의 「비캐시 input」으로 그대로 옮겼다. 그래서 캐시 읽기 토큰이 input 단가($5/M)로 한 번, cache_read 단가($0.5/M)로 또 한 번 계산됐다 |

**고친 것**(이 PR): 데몬이 hermes 의 usage 를 계약 모양으로 정규화한다 — `input = inputTokens − cachedRead − cachedWrite`, 그리고 누적 보고는 더하지 않고 **취한다**.
**효과**: 게임 제작 방 hermes 40 attempt 기준 **$1,544 → $352(−77%)**, 방 합계 **$2,743 → $1,551(−43%)**. 이것은 실제로 덜 쓰는 것이 아니라 **잘못 매긴 값을 바로잡는 것**이다 — 예산 강제(FR-7.3)와 비용 보고가 4.4배 과대였다.

---

## 1. (1) hermes 는 캐시를 쓴다 — 코드와 실측

### 설정 (`~/.hermes/config.yaml`, 읽기만 함)

```yaml
model: {default: claude-opus-5, provider: anthropic}
prompt_caching:
  cache_ttl: 5m
  long_lived_prefix: true
  long_lived_ttl: 1h
```

### 코드

- `agent/prompt_caching.py` — 기본 레이아웃은 **cache_control breakpoint 4개**: 정적 시스템 접두, 시스템 프롬프트 끝, 마지막 비시스템 메시지 2개. 정적 접두를 못 찾으면 시스템 1 + 마지막 메시지 3으로 물러선다. 모델·경로별 TTL 클램프(`effective_cache_ttl`)가 있고 Anthropic 네이티브 경로는 `1h` 를 그대로 쓴다.
- 즉 **매 턴 대화 꼬리에 breakpoint 를 새로 찍는** 방식이라, 앞쪽(시스템 + 지난 턴 전부)이 캐시 접두가 된다.

### 실측 (`hermes acp`, 임시 폴더, 같은 세션 2턴, 비용 수 센트)

도구: `05-hermes-cache-probe.py`(이 디렉터리). 데몬과 같은 stdio JSON-RPC 로 `initialize → session/new → session/set_model → session/prompt ×2`.

| 턴 | inputTokens | cachedReadTokens | outputTokens | totalTokens |
|---|---|---|---|---|
| 1 | 26,248 | 0 | 5 | 26,253 |
| 2 | **52,535** | **26,244** | 9 | 52,544 |

이 네 줄이 §2 의 두 사실을 그대로 준다.

---

## 2. (3) 원인 — `inputTokens` 의 뜻이 계약과 다르다

### (a) `inputTokens` 는 캐시를 **포함**한다

두 행 모두 `totalTokens = inputTokens + outputTokens` 다. 캐시 읽기가 input 의 **형제가 아니라 부분**이라는 뜻이다. 소스에서도 같다:

- `acp_adapter/server.py:2209` — `Usage(input_tokens=result.get("prompt_tokens", 0), …, cached_read_tokens=result.get("cache_read_tokens"))`
- `agent/usage_pricing.py` — `CanonicalUsage.prompt_tokens = input_tokens + cache_read_tokens + cache_write_tokens`

우리 계약의 `input` 은 **비캐시 input** 이다(서버가 `input × 입력단가 + cache_read × 읽기단가` 로 매긴다, `server/internal/cost`). 그래서 지금까지 캐시 토큰마다 $5/M + $0.5/M 을 냈다 — 제값 $0.5/M 의 **11배**.

이것이 0단계에서 hermes 가 「캐시가 없는 모양」으로 보인 이유다. 캐시 읽기는 있었고, `input` 안에 숨어 있었다.

### (b) `inputTokens` 는 어댑터 세션에 대해 **누적**이다

턴 2의 52,535 = 턴 1의 26,248 + 턴 2 자신의 26,287. 어댑터 프로세스는 attempt 하나만 살기 때문에 보고값이 곧 attempt 총계라 평소에는 맞지만, **거절 재시도(D-13)** 는 한 attempt 안에서 프롬프트를 두 번 보낸다 — 거기서 더하면 첫 턴을 두 번 센다(그 턴이 재개 프롬프트라 제일 비싸다).

### (c) `cachedWriteTokens` 는 오지 않는다

ACP 스키마에는 칸이 있고(`acp/schema.py` `Usage.cached_write_tokens`) hermes 내부도 `cache_write_tokens` 를 추적하는데(`agent/turn_finalizer.py`), 어댑터가 `cached_read_tokens` 만 채운다(`server.py:2213`). 실측 응답에도 `cachedWriteTokens` 키 자체가 없다. **upstream 갭**이며 데몬이 만들어낼 수 없다 — §5 제안.

---

## 3. (2) 우리 hermes 경로는 접두를 깨지 않는다

| 자리 | 모양 | 캐시에 미치는 영향 |
|---|---|---|
| 브리프 | `<workdir>/COLAB_BRIEF.md` 파일(`daemon/internal/brief`) — 턴 프롬프트 첫 줄이 경로를 가리킨다 | **접두에 없다.** 파일 내용이 턴마다 바뀌어도 프롬프트 접두는 안 바뀐다(에이전트가 읽으면 그 결과가 꼬리에 붙을 뿐) |
| 턴 프롬프트 | `session/prompt` 의 사용자 메시지 = 대화의 **꼬리** | 앞선 턴 전부가 접두로 남는다 → 캐시 적중 |
| 모델 | `session/set_model`(`modelId`) | 세션당 1회 |

claude_code 는 브리프가 `_meta.systemPrompt` 로 가기 때문에 [6]·[7] 이 바뀌면 **접두가 통째로** 무효가 됐다(04 §3, 놓침 69/70). hermes 는 브리프가 시스템 프롬프트에 없으므로 같은 병이 없다.

실사용 수치가 이를 뒷받침한다 — 게임 제작 방 hermes 40 attempt 의 캐시 적중률(캐시 읽기 ÷ 프롬프트 토큰): **중앙 0.916**, 최소 0.241, 최대 0.977.

> 곁가지: 04 의 1단계 제안 ①([6]·[7] 을 턴 프롬프트로)은 claude_code 전용 처방이고, hermes 에는 이미 그 모양이 적용돼 있는 셈이다. 그 제안의 근거가 하나 늘었다.

---

## 4. 고친 자리와 검증

`daemon/internal/harness/acp/hermes_usage.go`(새 파일) + `runner.go` `recordUsage` 한 갈래:

```go
input := u.InputTokens - u.CachedReadTokens - u.CachedWriteTokens  // 음수는 0 으로
// hermes: 누적 보고 → 더하지 않고 취한다. claude_code: 종전대로 더한다.
```

claude_code 경로는 **건드리지 않았다** — 그쪽 숫자는 두 성질 어느 쪽도 측정된 바 없고, 0단계 비용 적합이 지금 읽는 그대로 실측 비용을 재현한다(04 §3).

테스트 4건 + **주입 5종 전부 RED**:

| 주입 | 결과 |
|---|---|
| 캐시 분리 제거(옛 모양) | RED — `in 52535`(이중 과금 복귀) |
| 누적을 더하기로 | RED — 거절 재시도에서 `in 52539 / out 14` |
| cache_read 버리기 | RED |
| 클램프 제거 | RED — `in -850`(task_usage CHECK 위반 경로) |
| 분리를 claude 에도 적용 | RED — claude `in 0` |

## 5. 남는 것 · 제안

1. **`cachedWriteTokens` 는 여전히 0 이다** — 어댑터가 채우지 않는다. 캐시 **쓰기**가 얼마인지 모르므로 hermes 세션의 캐시 갱신 비용은 아직 안 보인다. 제안: hermes upstream 에 `acp_adapter/server.py` 의 `Usage(...)` 에 `cached_write_tokens=result.get("cache_write_tokens")` 한 줄(이미 `turn_finalizer` 가 값을 갖고 있다). 그때까지 hermes attempt 의 cache_write 는 0 으로 읽고, 비용은 그만큼 과소 추정이다(읽기 대비 작은 항이라 방향은 안전한 쪽 — 지금의 4.4배 과대와 반대).
2. **설정은 바꾸지 않았다.** `~/.hermes/config.yaml` 은 읽기만 했다. `long_lived_ttl: 1h` 가 이미 켜져 있어 바꿀 이유도 없었다.
3. **0단계 기준선 §1 의 hermes 비용 행은 이 수정 뒤 다시 읽어야 한다** — 04 는 「기록된 값」을 그대로 인용했고, 그 값이 4.4배였다. `04-baseline.md` 는 아직 dev 에 없어(PR #382) 이 브랜치에서 고치지 않았다 — **#382 가 머지되면 §1 표에 주석 한 줄을 다는 것이 남은 일**이다. 과거 `task_usage` 행을 소급 재계산하지는 않았다(측정값이 아니라 가격 계산의 문제이고, 재계산은 행을 다시 쓰는 일이라 별건 — 판단은 Lead).
4. **계측(0단계 A)이 이제 이것을 잡는다**: `task_context_metric.cache_write` 와 `samples` 가 attempt 마다 남으므로, 다음 hermes 턴부터는 적중률·쓰기 비중을 SQL 한 번으로 본다.

## 6. 한계

- 실측은 **2턴 · 짧은 프롬프트 1회**다. 캐시 TTL(5m/1h)이 hermes 경로에서 실제로 얼마인지는 재지 않았다(지연 읽기를 하려면 5분 넘게 놀려야 해 이번 범위 밖).
- 게임 제작 방 수치는 **사본**(live-snapshot5)이다. 실사용 DB 는 조회하지 않았다.
- `cachedWriteTokens` 가 없으므로 「고친 뒤 $352」는 **캐시 쓰기 0 가정**이다. 쓰기가 실제로 프롬프트의 5~10%라면 수십 달러가 더해진다 — 그래도 $1,544 와는 자릿수가 다르다.
- hermes 0.20.6 · `anthropic:claude-opus-5-5` 한 조합만 봤다. 다른 provider(OpenRouter 등)는 `prompt_caching.py` 가 다른 레이아웃을 쓰므로 같은 결론을 보장하지 않는다.
