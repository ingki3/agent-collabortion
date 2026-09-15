"use client";
/**
 * S14 Settings(SCREEN §4.10) — 탭 8개(멤버 · 컴퓨터 정책 · 예산 · 루프 상한 · 컨텍스트 · 작업 폴더 · 보안 · 알림) +
 * 9번째 「대시보드」(PRD §11 지표 10개, G9). 권한 열대로 owner·admin 만 저장하고 멤버는 읽기 + 비활성 사유(DisabledHint).
 *
 * 탭은 `?tab=` 쿼리다 — `router.push` 로 바꾸므로 뒤로가기가 이전 탭으로 돌아간다. 내비에는 owner·admin 만 보이지만
 * URL 로 직접 오면 숨기지 않고 사유를 보인다(U13 변형). 「화면」(테마)은 **탭 밖 상단** — 이 브라우저의 표시 설정이라
 * 워크스페이스 권한과 무관하다(§8.3). 읽기 전용 멤버도 자기 화면 밝기는 고른다.
 */
import { Suspense, useCallback, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth/AuthContext";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { ThemeSelect } from "@/components/ThemeSelect";
import { PageHead } from "@/components/PageHead";
import { MembersTab } from "@/components/MembersTab";
import { MetricsTab } from "@/components/MetricsTable";
import { NotificationsTab, WorkspaceSettingsTab, type WorkspaceTab } from "@/components/SettingsTabs";
import { DEFAULT_TAB, isSettingsTab, SETTINGS_TABS, type SettingsTab } from "@/lib/settings";
import type { NotificationSettings, WorkspaceSettings, WorkspaceSettingsUpdate } from "@/lib/api/types";
import "@/components/settings.css";

const WHO_LABEL = { admin: "소유자·관리자", owner: "소유자", personal: "개인", read: "읽기" } as const;

export default function SettingsPage() {
  // `useSearchParams` 는 정적 경로에서 Suspense 경계가 필요하다(next build) — 로그인 화면과 같은 모양.
  return (
    <Suspense fallback={null}>
      <SettingsInner />
    </Suspense>
  );
}

function SettingsInner() {
  const { canManage, workspace, me } = useAuth();
  const router = useRouter();
  const search = useSearchParams();
  const raw = search.get("tab");
  const tab: SettingsTab = isSettingsTab(raw) ? raw : DEFAULT_TAB;
  const role = workspace?.my_role ?? null;
  const go = (t: SettingsTab) => router.push(`/settings?tab=${t}`);

  const [settings, setSettings] = useState<WorkspaceSettings | null>(null);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notif, setNotif] = useState<NotificationSettings | null>(null);
  const [notifError, setNotifError] = useState<string | null>(null);

  const loadSettings = useCallback(async () => {
    if (!workspace) return;
    try {
      setSettings(await api.get("/workspaces/{workspaceId}/settings", { path: { workspaceId: workspace.id } }));
      setSettingsError(null);
    } catch (e) {
      setSettingsError(errorMessage(e));
    }
  }, [workspace]);
  const loadNotif = useCallback(async () => {
    try {
      setNotif(await api.get("/me/notification-settings"));
      setNotifError(null);
    } catch (e) {
      setNotifError(errorMessage(e));
    }
  }, []);
  const isWorkspaceTab = tab !== "members" && tab !== "notifications" && tab !== "dashboard";
  useEffect(() => { if (isWorkspaceTab) void loadSettings(); }, [isWorkspaceTab, loadSettings]);
  useEffect(() => { if (tab === "notifications") void loadNotif(); }, [tab, loadNotif]);

  async function saveSettings(patch: WorkspaceSettingsUpdate): Promise<WorkspaceSettings | null> {
    if (!workspace) return null;
    setFieldErrors({});
    setSettingsError(null);
    try {
      const next = await api.patch("/workspaces/{workspaceId}/settings", { path: { workspaceId: workspace.id }, body: patch });
      setSettings(next);
      return next;
    } catch (e) {
      // 422 는 필드별 문장(`errors[]`)을 그 칸 옆에, 나머지는 위에 한 줄.
      if (isApiError(e) && e.status === 422 && e.problem.errors?.length) {
        setFieldErrors(Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message])));
      } else {
        setSettingsError(errorMessage(e));
      }
      return null;
    }
  }
  async function saveNotif(next: NotificationSettings): Promise<NotificationSettings | null> {
    setNotifError(null);
    try {
      const out = await api.patch("/me/notification-settings", { body: next });
      setNotif(out);
      return out;
    } catch (e) {
      setNotifError(errorMessage(e));
      return null;
    }
  }

  return (
    <div className="content--narrow" data-testid="settings-page" data-tab={tab}>
      <PageHead screen="settings" />
      {!canManage && <p className="notice" data-testid="settings-readonly">소유자·관리자만 워크스페이스 설정을 바꿀 수 있습니다 — 읽고, 알림과 화면은 직접 고를 수 있습니다.</p>}
      <section className="card stack" data-testid="settings-appearance" style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0, fontSize: "var(--fs-card)" }}>화면</h2>
        <ThemeSelect />
      </section>

      <nav className="tabs" role="tablist" aria-label="설정 탭" data-testid="settings-tabs">
        {SETTINGS_TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            className="tabs__tab"
            aria-selected={t.key === tab}
            onClick={() => go(t.key)}
            data-testid={`tab-${t.key}`}
          >
            {t.label}
            <span className="tabs__who">{WHO_LABEL[t.who]}</span>
          </button>
        ))}
      </nav>

      {tab === "members" && workspace && <MembersTab workspaceId={workspace.id} myRole={role} meUserId={me?.user.id ?? null} />}
      {tab === "dashboard" && workspace && <MetricsTab workspaceId={workspace.id} />}
      {tab === "notifications" && <NotificationsTab settings={notif} onSave={saveNotif} error={notifError} />}
      {isWorkspaceTab && (
        <>
          {settingsError && <p className="problem" role="alert" data-testid="settings-error">{settingsError}</p>}
          {!settings && !settingsError && <p className="muted">불러오는 중…</p>}
          {settings && (
            <WorkspaceSettingsTab tab={tab as WorkspaceTab} settings={settings} role={role} onSave={saveSettings} fieldErrors={fieldErrors} />
          )}
        </>
      )}
    </div>
  );
}
