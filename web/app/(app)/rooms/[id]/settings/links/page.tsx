"use client";
/** S24 참고 방 링크(`/rooms/:id/settings/links`) — S20 위에 뜨는 다이얼로그(SCREEN §4.12). 닫으면 방 설정으로. */
import { useParams, useRouter } from "next/navigation";
import { RoomLinksDialog } from "@/components/RoomLinksDialog";
import { RoomSettingsForm } from "@/components/RoomSettingsForm";

export default function RoomLinksPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  return (
    <>
      <RoomSettingsForm roomId={id} onDeleted={(r) => router.push(`/rooms?deleted=${encodeURIComponent(r.name)}`)} />
      <RoomLinksDialog roomId={id} onClose={() => router.push(`/rooms/${id}/settings`)} />
    </>
  );
}
