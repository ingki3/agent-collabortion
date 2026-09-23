"use client";
/**
 * /dev/room-dialogs?room=<id>&work=new — **개발 전용**(page.dev.tsx, 프로덕션 빌드에 없다). S7 재작성(T-R2-W2)이 `RoomQueryDialogs` 를
 * 방 화면에 마운트하기 전까지 S21·S26 을 실제 앱 껍데기(로그인·내비·실시간) 안에서 띄워 보는 자리다 — 스크린샷
 * (`e2e/r2-w3-shots.sh`)과 손 검증용. 쿼리는 S7 과 같다: `work=new` · `work=from&message=` · `work_proposal=`.
 */
import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { RoomQueryDialogs } from "@/components/RoomQueryDialogs";

function Inner() {
  const room = useSearchParams().get("room") ?? "";
  return (
    <div data-testid="dev-room-dialogs" data-room-id={room}>
      <p className="muted small">/dev/room-dialogs — {room}</p>
      {room && <RoomQueryDialogs roomId={room} />}
    </div>
  );
}

export default function DevRoomDialogsPage() {
  return (
    <Suspense fallback={null}>
      <Inner />
    </Suspense>
  );
}
