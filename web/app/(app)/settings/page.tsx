"use client";
/** S14 Settings — P2 이후. 내비 자리만 둔다(SCREEN §3.2). 권한 없음도 숨기지 않고 사유를 보인다(§7). */
import { useAuth } from "@/lib/auth/AuthContext";

export default function SettingsPage() {
  const { canManage } = useAuth();
  return (
    <div>
      <div className="page-head"><h1>Settings</h1></div>
      {!canManage && <p className="notice">owner·admin 만 설정을 바꿀 수 있습니다.</p>}
      <div className="empty" data-testid="placeholder-settings">
        <div className="empty__title">S14 Settings 은 P2 에서 구현됩니다</div>
        <div className="empty__body">이 자리는 앱 셸 내비 항목을 위한 것입니다.</div>
      </div>
    </div>
  );
}
