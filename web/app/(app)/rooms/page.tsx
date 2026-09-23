"use client";
/** S5 방 목록 · S25 방 찾기(`/rooms?q=&unread=&archived=`) — 본체는 `RoomsView`. */
import { Suspense } from "react";
import { RoomsView } from "./RoomsView";

export default function RoomsPage() {
  // `useSearchParams` 는 정적 경로에서 Suspense 경계가 필요하다(next build).
  return (
    <Suspense fallback={null}>
      <RoomsView />
    </Suspense>
  );
}
