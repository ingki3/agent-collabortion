/**
 * 방 다이얼로그 화면 테스트(T-R2-W3)의 공용 준비 — 목 서버에 fetch 다리를 놓고, `useAuth` 가 돌려줄 사람을 바꾼다.
 * (각 테스트 파일이 `vi.mock("@/lib/auth/AuthContext")` 로 `auth` 를 돌려주게 한다 — vi.mock 은 파일마다 적어야 호이스팅된다.)
 */
import { installFetchBridge, type FetchBridge } from "./fetch-bridge";
import { store } from "./store";
import type { Room } from "@/lib/api/types";

export const auth: { me: unknown; workspace: unknown; canManage: boolean } = { me: null, workspace: null, canManage: false };
export const uid = (email: string) => [...store().users.values()].find((u) => u.email === email)!.id;

export async function setup(roomName = "결제팀"): Promise<{ bridge: FetchBridge; roomId: string; wsId: string; as: (email: string) => Promise<void> }> {
  const bridge = await installFetchBridge();
  const as = async (email: string) => {
    await bridge.login(email);
    const u = [...store().users.values()].find((x) => x.email === email)!;
    const m = store().members.find((x) => x.user.id === u.id)!;
    auth.me = { user: { id: u.id, email: u.email, display_name: u.display_name } };
    auth.workspace = { id: m.workspace_id, my_role: m.role };
    auth.canManage = m.role === "owner" || m.role === "admin";
  };
  await as("demo@colab.dev");
  const wsId = store().members[0].workspace_id;
  const roomId = (await bridge.call<Room>("POST", `/workspaces/${wsId}/rooms`, { name: roomName })).body.id;
  return { bridge, roomId, wsId, as };
}
