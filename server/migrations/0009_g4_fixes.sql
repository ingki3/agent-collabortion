-- 0009_g4_fixes.sql — G4 통합(T-I2)이 실서버에서 격리한 결함들의 저장 자리 (T-S4)
--
-- 0001~0008 은 건드리지 않는다. 여기서 더하는 것은 계약이 이미 노출하라고 말하는데
-- 서버에 적을 칸이 없어 늘 null 이던 값들이다.

-- ---------------------------------------------------------------------------
-- probe 최상위 colab_cli (daemon-protocol §3, v0.5)
-- ---------------------------------------------------------------------------
-- 에이전트는 colab CLI 로만 서버에 말한다. CLI 가 없는 머신은 "조용히 아무 말도
-- 못 하는" 상태가 되는데, 데몬은 이미 probe 로 보고하고 있었고 계약 Runtime 에도
-- 필드가 있었는데 서버가 버리고 있었다(항상 null). capabilities[] 가 아니라 별도
-- 컬럼인 이유는 계약과 같다 — 런타임 속성이 아니라 머신 속성이다.
-- NULL 은 "아직 probe 를 못 받았다"이고 `{present:false}` 와 다르다.
ALTER TABLE runtime ADD COLUMN colab_cli jsonb;
ALTER TABLE runtime ADD CONSTRAINT runtime_colab_cli_check
    CHECK (colab_cli IS NULL OR (colab_cli ? 'present' AND colab_cli ? 'version'));

-- ---------------------------------------------------------------------------
-- workdir: 데몬 보고를 행으로 남긴다 (daemon-protocol §6, FR-6.1/6.4)
-- ---------------------------------------------------------------------------
-- 계약 Workdir.dirty("미병합 커밋 또는 미커밋 변경") 를 담을 칸이 없었다. GC 차단
-- 판정(E13-09~13)의 입력이므로 보고를 받는 순간 적어 둔다.
ALTER TABLE workdir ADD COLUMN dirty boolean;

-- §6 보고는 (session_id, path) 로 같은 디렉토리를 다시 말한다 — 데몬은 행 id 를
-- 모른다(WorkdirsRequest 에 id 가 없다). 재보고가 행을 늘리지 않도록 이 쌍을
-- 업서트 키로 쓴다.
CREATE UNIQUE INDEX workdir_session_path ON workdir (session_id, path_or_ref);
