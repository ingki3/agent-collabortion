-- 0008_artifact_blob_gc.sql — 아티팩트 행이 사라지면 본문도 사라진다 (T-S3 리뷰 R1)
--
-- 0007 이 본문을 large object 에 두면서 **정리 경로를 두지 않았다.** artifact 는
-- session 에 ON DELETE CASCADE 로 매달려 있으므로(0001:418) 세션이 지워지면 행은
-- 사라지고 바이트는 pg_largeobject 에 영원히 남는다. P2 에는 삭제 API 가 없어
-- 당장 밟지 않지만, FR-6.4 GC·세션 보존 기한이 들어오는 순간 세션마다
-- 최대 50MB × 버전 수의 고아가 쌓인다. 저장 방식을 정한 쪽이 정리도 같이 둔다.
--
-- 왜 트리거인가: CASCADE 삭제는 애플리케이션 코드를 거치지 않는다. 서비스 층에
-- 두면 세션 삭제 경로에서만 새어 나가고, 그 경로는 아직 코드로 존재하지도 않는다.
-- 트리거는 CASCADE 와 **같은 트랜잭션**에서 돌므로 롤백도 함께 된다.

CREATE FUNCTION artifact_unlink_blob() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- 저장 방식은 storage_ref 접두가 스스로 말한다. 다른 저장소(S3 등)가 들어와도
    -- 이 트리거가 깨지지 않도록, pglo: + 숫자 oid 인 것만 건드린다.
    IF OLD.storage_ref ~ '^pglo:[0-9]+$' THEN
        PERFORM lo_unlink(substr(OLD.storage_ref, 6)::oid);
    END IF;
    RETURN OLD;
EXCEPTION
    -- 이미 없는 blob 을 지우는 것은 오류가 아니다(중복 정리·수동 복구 후).
    -- 여기서 예외가 새면 세션 삭제 전체가 실패한다 — 정리가 삭제를 막으면 안 된다.
    WHEN undefined_object THEN
        RETURN OLD;
END;
$$;

CREATE TRIGGER artifact_unlink_blob_trg
    AFTER DELETE ON artifact
    FOR EACH ROW EXECUTE FUNCTION artifact_unlink_blob();
