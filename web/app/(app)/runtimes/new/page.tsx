"use client";
/** S12 Add a computer — 단독 화면. 준비 완료되면 Runtimes 로. */
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PairingPanel } from "@/components/PairingPanel";
import { useAuth } from "@/lib/auth/AuthContext";

export default function AddComputerPage() {
  const router = useRouter();
  const { workspace, canManage } = useAuth();
  if (!workspace) return null;
  return (
    <div className="content--narrow">
      <div className="page-head">
        <h1>Add a computer</h1>
        <Link href="/runtimes" className="btn btn--ghost btn--sm">
          Runtimes 로
        </Link>
      </div>
      <PairingPanel workspaceId={workspace.id} canManage={canManage} onReady={() => setTimeout(() => router.push("/runtimes"), 1500)} />
    </div>
  );
}
