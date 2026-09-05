"use client";
/**
 * 인증 상태 + 워크스페이스 컨텍스트(SCREEN §2.1).
 * - 앱 셸 진입 시 GET /me 한 번. 401 이면 /login?return_to=<원래 URL> 로 보낸다(원래 URL 기억).
 * - 워크스페이스가 없으면 /onboarding 으로 강제 이동. 있으면 마지막으로 고른 것(localStorage) 또는 첫 번째.
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { api, isApiError } from "@/lib/api/client";
import type { Me, WorkspaceWithRole } from "@/lib/api/types";

const WS_KEY = "colab.workspace";

export interface AuthState {
  me: Me | null;
  loading: boolean;
  workspace: WorkspaceWithRole | null;
  /** owner·admin — Settings 내비 노출, 런타임 추가 권한(SCREEN §2.3). */
  canManage: boolean;
  selectWorkspace: (id: string) => void;
  refresh: () => Promise<Me | null>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

export function loginUrlFor(pathname: string): string {
  return pathname && pathname !== "/" ? `/login?return_to=${encodeURIComponent(pathname)}` : "/login";
}

export function AuthProvider({
  children,
  requireWorkspace = true,
}: {
  children: React.ReactNode;
  /** false 면(온보딩 화면) 워크스페이스가 없어도 리다이렉트하지 않는다. */
  requireWorkspace?: boolean;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const [me, setMe] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);
  const [wsId, setWsId] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const m = await api.get("/me");
      setMe(m);
      return m;
    } catch (e) {
      if (isApiError(e) && e.status === 401) {
        setMe(null);
        router.replace(loginUrlFor(pathname));
        return null;
      }
      throw e;
    } finally {
      setLoading(false);
    }
  }, [pathname, router]);

  useEffect(() => {
    void refresh();
    // 최초 1회만 — pathname 변화마다 /me 를 다시 부르지 않는다.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    try {
      setWsId(window.localStorage.getItem(WS_KEY));
    } catch {
      /* private mode 등 */
    }
  }, []);

  const workspace = useMemo(() => {
    if (!me) return null;
    return me.workspaces.find((w) => w.id === wsId) ?? me.workspaces[0] ?? null;
  }, [me, wsId]);

  useEffect(() => {
    if (!loading && me && requireWorkspace && me.workspaces.length === 0 && pathname !== "/onboarding") {
      router.replace("/onboarding");
    }
  }, [loading, me, requireWorkspace, pathname, router]);

  const selectWorkspace = useCallback((id: string) => {
    setWsId(id);
    try {
      window.localStorage.setItem(WS_KEY, id);
    } catch {
      /* ignore */
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post("/auth/logout");
    } finally {
      setMe(null);
      router.replace("/login");
    }
  }, [router]);

  const value = useMemo<AuthState>(
    () => ({
      me,
      loading,
      workspace,
      canManage: workspace?.my_role === "owner" || workspace?.my_role === "admin",
      selectWorkspace,
      refresh,
      logout,
    }),
    [me, loading, workspace, selectWorkspace, refresh, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}

/** 워크스페이스가 확정된 화면 전용 — 없으면 throw 대신 null 을 돌려 화면이 로딩을 그리게 한다. */
export function useWorkspace(): WorkspaceWithRole | null {
  return useAuth().workspace;
}
