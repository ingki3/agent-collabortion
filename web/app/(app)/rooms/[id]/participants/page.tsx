"use client";
/**
 * S19 참여자 초대·퇴장(`/rooms/:id/participants`) — 방 화면 위에 뜨는 다이얼로그의 **주소**(SCREEN §3.2 「다이얼로그에도 URL 을 준다」).
 * 받은 요청(`room_invited`)·시스템 메시지에서 바로 여기로 온다. 닫으면 방으로, 이 방에서 나가면 방 목록으로.
 * S7 안에서 여는 버튼은 같은 컴포넌트(`RoomParticipantsDialog`)를 직접 띄워도 된다(T-R2-W2).
 */
import { useParams, useRouter } from "next/navigation";
import { RoomParticipantsDialog } from "@/components/RoomParticipantsDialog";

export default function RoomParticipantsPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  return <RoomParticipantsDialog roomId={id} onClose={() => router.push(`/rooms/${id}`)} onLeft={() => router.push("/rooms")} />;
}
