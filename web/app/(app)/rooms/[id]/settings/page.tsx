"use client";
/** S20 방 설정(`/rooms/:id/settings`, 페이지) — SCREEN §4.11. 삭제되면 방 목록으로(안내 한 줄은 S5 가 `?deleted=` 로 그린다). */
import { useParams, useRouter } from "next/navigation";
import { RoomSettingsForm } from "@/components/RoomSettingsForm";

export default function RoomSettingsPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  return <RoomSettingsForm roomId={id} onDeleted={(r) => router.push(`/rooms?deleted=${encodeURIComponent(r.name)}`)} />;
}
