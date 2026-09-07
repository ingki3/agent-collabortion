-- 0019_p4_g7_blockers.sql — G7 1판 차단 결함 (T-S10: S-55)
--
-- `runtime.workdir_root` — probe(§3)가 매번 보내는데 저장할 자리가 없었다.
--
-- 왜 이 칸이 차단 결함인가. daemon-protocol v0.7.3 §4.1 은 TaskBundle 의
-- `workdir.path` 를 **절대 경로**로 못박았고, 그 절대 경로를 조립할 수 있는
-- 유일한 재료가 이 값이다(서버는 사용자 머신의 디렉터리 구조를 달리 알 방법이
-- 없다). 저장하지 않는 동안 서버는 `path.Join("", <session>, <agent>)` 로
-- **상대** 경로를 실었고, 데몬은 그것을 자기 CWD 기준으로 절대화해
-- (a) 사용자 저장소 **안**에 워크트리를 만들고 (b) 없는 디렉터리를 런타임의
-- cwd 로 넘겨 `worktree` 세션이 첫 턴부터 전부 `failed(config)` 로 죽었다
-- (T-I4 실측, plan/G7_REPORT.md §1 차단 ①).
--
-- NULL 은 "probe 를 아직 못 받았다" 이고, 그 상태에서는 번들을 만들지 않는다
-- (queue.buildBundle 의 errNoWorkdirRoot). 조용한 상대 경로보다 시끄러운
-- 실패가 낫다 — 위 실측이 그 이유다.
ALTER TABLE runtime ADD COLUMN IF NOT EXISTS workdir_root text;

COMMENT ON COLUMN runtime.workdir_root IS
  'daemon-protocol §3 probe `workdir_root`. §4.1 TaskBundle.workdir.path 의 절대 경로 조립 재료(v0.7.3). NULL = probe 미수신 → 번들 생성 거부.';
