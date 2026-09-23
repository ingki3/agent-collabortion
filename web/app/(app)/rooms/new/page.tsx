"use client";
/** S18 방 만들기(`/rooms/new`) — S5 위에 뜨는 모달이다(SCREEN §3.2 「다이얼로그에도 URL 을 준다」). 닫으면 `/rooms`. */
import { Suspense } from "react";
import { RoomsView } from "../RoomsView";

export default function NewRoomPage() {
  return (
    <Suspense fallback={null}>
      <RoomsView creating />
    </Suspense>
  );
}
