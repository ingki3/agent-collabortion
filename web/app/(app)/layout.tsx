"use client";
import { AuthProvider } from "@/lib/auth/AuthContext";
import { Shell } from "@/components/Shell";

/** 앱 셸 화면 전부(S5~S14)는 인증 + 워크스페이스가 필요하다(SCREEN §2.1). */
export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <AuthProvider>
      <Shell>{children}</Shell>
    </AuthProvider>
  );
}
