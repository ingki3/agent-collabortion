"use client";
/** S23 맥락 읽기 기록(`/rooms/:id/reads`, 페이지) — SCREEN §4.13. */
import { useParams } from "next/navigation";
import { RoomReadsTable } from "@/components/RoomReadsTable";

export default function RoomReadsPage() {
  const { id } = useParams<{ id: string }>();
  return <RoomReadsTable roomId={id} />;
}
