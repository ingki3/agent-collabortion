-- 0018_session_rebind_prompt.sql — 재바인딩 첫 프롬프트를 실어 나를 자리 (S-53)
--
-- `rebindSession`(openapi)은 "`worktree`면 새 workdir을 만들고 **첫 프롬프트에**
-- 'diff 아티팩트 N개를 제출 순서대로 적용한 뒤 이어가라'를 넣는다(E14-06)"이다.
-- P4(#162)는 그 문장을 `runtimes.RebindPrompt`로 만들어 `RebindPlan.Prompt`에
-- 담았지만 아무 데도 저장하지 않았고, `queue.buildBundle`은 그런 것이 있는 줄도
-- 몰랐다 — 골든이 `PromptSaysApplyArtifacts`·`PromptArtifactOrder` 같은 **판정
-- 필드**만 재기 때문에 표는 초록인 채로 프롬프트가 에이전트에게 한 번도 가지
-- 않았다.
--
-- 세션에 두는 이유: 재바인딩은 세션 단위 조작이고(`POST /sessions/{id}/rebind`),
-- 그 세션의 어느 lane 이 먼저 깨어나든 새 workdir 은 비어 있다.
-- 비우는 시점은 그 attempt 의 `completed` finish 다 — claim 에서 비우면 재큐잉
-- (E5-03 runtime_offline) 한 번에 diff 재적용 지시가 사라지고 콜드 스타트 문장만
-- 남아 E14-06 이 조용히 깨진다.
ALTER TABLE session ADD COLUMN IF NOT EXISTS rebind_prompt text;

COMMENT ON COLUMN session.rebind_prompt IS
  'FR-9.2/E14-06: 재바인딩 뒤 턴 프롬프트에 실을 <rebind> 지시. completed finish 에서 NULL 로 돌아간다.';
