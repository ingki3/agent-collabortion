-- 0005_task_cancelled_failure_kind — task.failure_kind를 cancelled 상태에서도 허용한다 (openapi cancelLane, EVAL E10-04)
--
-- 결함(G3 S-2): 0001의 CHECK는 failure_kind를 status = 'failed'에서만 허용했다. 계약(openapi
-- cancelLane)은 "task `cancelled`(`failure_kind: cancelled`)"이고 lane은 `failed(cancelled)`로
-- 분류된다(SCREEN §4.5). lane에는 failure_kind 컬럼이 없어 서버가 lane의 현재 task에서
-- 파생하므로, cancelled task가 failure_kind = 'cancelled'를 가져야 한다.
--
-- 0001~0004는 수정하지 않는다. failure_kind를 참조하는 CHECK를 이름에 기대지 않고 찾아
-- 드롭한 뒤 failed·cancelled 둘 다 허용하는 CHECK로 재생성한다.

DO $$
DECLARE
    c record;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'task'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%failure_kind IS NULL%'
    LOOP
        EXECUTE format('ALTER TABLE task DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE task ADD CONSTRAINT task_failure_kind_check
    CHECK (status IN ('failed', 'cancelled') OR failure_kind IS NULL);

-- 이미 cancelled로 끝난 task는 계약대로 failure_kind = 'cancelled'로 맞춘다.
UPDATE task SET failure_kind = 'cancelled' WHERE status = 'cancelled' AND failure_kind IS NULL;
