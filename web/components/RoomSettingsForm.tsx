"use client";
/**
 * S20 방 설정(`/rooms/:id/settings`) — SCREEN v0.19.2 §4.11, T-R2-W3. **여기가 방의 유일한 안전망이다** — 기본값이 워크스페이스에서
 * 상속되므로 사람이 한 번도 안 보면 모든 방이 같은 값으로 돈다(FR-2.1).
 *
 * 맨 위 「이름·설명」(v0.19.5, PRD FR-2.1.2) — 두 칸 + 「저장」(바뀐 칸이 없으면 비활성, 바뀐 칸만 보낸다). 판정·문장은 S7 머리·S5 카드와 같은
 * `InlineTitleEdit` 의 것(`roomNameProblem` · `renameErrorText`)을 쓴다.
 * 묶음 여덟(공개 범위 · 컴퓨터·격리 · 한도 · 자율성 · 기본 Director · 방장·부방장 · 참고 방 링크 · 보관·삭제). **각 묶음에 바꿨을 때의 영향을
 * 한 줄로** 적는다. 값 저장은 묶음마다(즉시 반영, `updateRoom` 부분 PATCH).
 *   - 공개 범위를 `invited` 로 바꾸면 「지금 이 방을 보는 사람 중 초대되지 않은 N명이 더는 볼 수 없게 됩니다」(수는 슬롯).
 *   - 컴퓨터·격리는 **첫 dispatch 전에만** — 고정된 뒤에는 읽기 전용 + 「〈컴퓨터〉에 고정됨 … [컴퓨터 바꾸기]」(S17). 판정은 서버의
 *     `Room.runtime_pinned`(계약 0.2.7) — 첫 실행 전에 미리 고른 컴퓨터(`runtime_id` 만 채워짐)는 아직 바꿀 수 있다. 저장이 `409 runtime_pinned`
 *     로 거절되면(그 사이 첫 실행이 났다) 그 자리에서 읽기 전용으로 바뀐다.
 *   - 방장 넘기기는 확인 다이얼로그(`TransferOwnerDialog`) + 시스템 메시지. 보관·삭제는 S5 카드 메뉴와 **같은 다이얼로그**(RoomDialogs).
 * 권한: 방장·부방장·ws owner·admin(삭제·방장 넘기기·부방장은 방장·ws owner·admin). 그 밖에게는 **읽기 전용** — 못 바꾸더라도 이 방이 어떤
 * 격리·예산으로 도는지는 봐야 한다(§1 원칙 5). 판정은 `Room.my_capabilities`.
 * 실시간: `room.updated`(첫 dispatch 순간 읽기 전용 · 다른 사람의 변경) · `room_link.updated`.
 */
import { useCallback, useEffect, useId, useState } from "react";
import Link from "next/link";
import { ConfirmDialog } from "./ConfirmDialog";
import { DisabledHint } from "./PageHead";
import { ArchiveRoomDialog, DeleteRoomDialog } from "./RoomDialogs";
import { renameErrorText, roomNameProblem } from "./InlineTitleEdit";
import { Slot } from "./Slot";
import { useRoomEvents } from "./RoomDialogShell";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import {
  AUTONOMY_TEXT, COMMON, losingViewers, personName, runtimePinned, SETTINGS, settingsGates, TRANSFER_DIALOG,
  type RoomLink, type RoomParticipant,
} from "@/lib/room-dialogs";
import { deleteRoomGate, ROOM_RENAME } from "@/lib/wording";
import type { AutonomyLevel, Member, Room, RoomUpdate, Runtime } from "@/lib/api/types";
import "./room-dialogs.css";

export interface RoomSettingsFormProps {
  roomId: string;
  /** 삭제된 뒤 — 호출부가 방 목록으로 보낸다. */
  onDeleted?: (room: { id: string; name: string }) => void;
}

type GroupKey = "name" | "visibility" | "runtime" | "limits" | "autonomy" | "director" | "deputy";
const SETTINGS_EVENTS = ["room.updated", "room_link.updated", "participant.joined", "participant.left"] as const;

export function RoomSettingsForm({ roomId, onDeleted }: RoomSettingsFormProps) {
  const { workspace } = useAuth();
  const [room, setRoom] = useState<Room | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [people, setPeople] = useState<RoomParticipant[]>([]);
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [links, setLinks] = useState<RoomLink[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState<GroupKey | null>(null);
  const [saved, setSaved] = useState<GroupKey | null>(null);
  const [groupError, setGroupError] = useState<{ group: GroupKey; text: string; fields: Record<string, string> } | null>(null);
  const [pinnedByServer, setPinnedByServer] = useState(false);
  const [transferring, setTransferring] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [unarchiveError, setUnarchiveError] = useState<string | null>(null);
  const base = useId();

  // 편집 중인 값(묶음마다). 방을 다시 받으면 저장하지 않은 편집은 버린다 — 다른 사람이 바꾼 값 위에 옛 편집을 덮지 않게.
  const [nameDraft, setNameDraft] = useState("");
  const [descDraft, setDescDraft] = useState("");
  const [vis, setVis] = useState<Room["visibility"]>("workspace");
  const [runtimeId, setRuntimeId] = useState<string>("");
  const [isoKind, setIsoKind] = useState<"none" | "worktree">("none");
  const [repo, setRepo] = useState("");
  const [budget, setBudget] = useState("");
  const [time, setTime] = useState("");
  const [works, setWorks] = useState("3");
  const [lanes, setLanes] = useState("5");
  const [autonomy, setAutonomy] = useState<AutonomyLevel>("guided");
  const [director, setDirector] = useState("");
  const [deputy, setDeputy] = useState("");

  const adopt = useCallback((r: Room) => {
    setRoom(r);
    setNameDraft(r.name);
    setDescDraft(r.description ?? "");
    setVis(r.visibility);
    setRuntimeId(r.runtime_id ?? "");
    setIsoKind(r.isolation?.kind === "worktree" ? "worktree" : "none");
    setRepo(r.isolation?.repo_path ?? "");
    setBudget(r.limits?.budget_usd != null ? String(r.limits.budget_usd) : "");
    setTime(r.limits?.time_limit ?? "");
    setWorks(String(r.limits?.max_concurrent_works ?? 3));
    setLanes(String(r.limits?.max_parallel_lanes ?? 5));
    setAutonomy(r.autonomy);
    setDirector(r.default_director_user_id ?? "");
    setDeputy(r.deputy_owner_user_id ?? "");
  }, []);

  const load = useCallback(async () => {
    try {
      const [r, parts, l] = await Promise.all([
        api.get("/rooms/{roomId}", { path: { roomId } }),
        api.get("/rooms/{roomId}/participants", { path: { roomId } }),
        api.get("/rooms/{roomId}/links", { path: { roomId } }).catch(() => ({ items: [] as RoomLink[] })),
      ]);
      adopt(r);
      setPeople(parts.items);
      setLinks(l.items);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [roomId, adopt]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!workspace) return;
    api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: workspace.id }, query: { limit: 100 } }).then((p) => setMembers(p.items ?? []), () => setMembers([]));
    api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }).then((r) => setRuntimes(r), () => setRuntimes([]));
  }, [workspace]);
  useRoomEvents(workspace?.id, roomId, SETTINGS_EVENTS, () => void load());

  if (!room) {
    return (
      <div className="rd-page" data-testid="rd-settings">
        <h1 className="rd-page__title">{SETTINGS.title}</h1>
        {error ? <p className="problem" role="alert" data-testid="rd-settings-error">{error}</p> : <p className="rd-hint">{COMMON.loading}</p>}
      </div>
    );
  }

  const gates = settingsGates(room);
  const canEdit = gates.configure.ok;
  const pinned = runtimePinned(room) || pinnedByServer;
  const pinnedRuntime = (runtimes ?? []).find((r) => r.id === room.runtime_id) ?? room.runtime;
  const chosenRuntime = (runtimes ?? []).find((r) => r.id === runtimeId);
  const humans = people.filter((p) => p.kind === "user" && p.user);
  const losing = losingViewers(members, people);
  const roHint = `${base}-ro`;
  const headHint = `${base}-head`;
  const delGate = deleteRoomGate({ my_room_role: room.my_room_role, active_work_count: room.counts?.works_active ?? 0 }, { canManage: false });
  const deleteOk = gates.delete.ok && (room.counts?.works_active ?? 0) === 0;

  async function save(group: GroupKey, patch: RoomUpdate) {
    setSaving(group);
    setSaved(null);
    setGroupError(null);
    try {
      adopt(await api.patch("/rooms/{roomId}", { path: { roomId }, body: patch }));
      setSaved(group);
    } catch (e) {
      if (isApiError(e) && e.code === "runtime_pinned") setPinnedByServer(true);
      const fields = isApiError(e) ? Object.fromEntries((e.problem.errors ?? []).map((x) => [x.field, x.message])) : {};
      setGroupError({ group, text: errorMessage(e), fields });
    } finally {
      setSaving(null);
    }
  }
  async function saveDeputy() {
    setSaving("deputy");
    setSaved(null);
    setGroupError(null);
    try {
      adopt(await api.put("/rooms/{roomId}/deputy", { path: { roomId }, body: { user_id: deputy || null } }));
      setSaved("deputy");
      void load();
    } catch (e) {
      setGroupError({ group: "deputy", text: errorMessage(e), fields: {} });
    } finally {
      setSaving(null);
    }
  }
  async function unarchive() {
    setUnarchiveError(null);
    try {
      adopt(await api.post("/rooms/{roomId}/unarchive", { path: { roomId } }));
    } catch (e) {
      setUnarchiveError(errorMessage(e));
    }
  }

  // 이름·설명 — 바뀐 칸만 보낸다(서버도 바뀐 칸에만 시스템 메시지를 남긴다). 판정은 앞뒤 공백을 뗀 값.
  const namePatch: RoomUpdate = {};
  if (nameDraft.trim() !== room.name) namePatch.name = nameDraft.trim();
  if (descDraft.trim() !== (room.description ?? "")) namePatch.description = descDraft.trim();
  const nameProblem = roomNameProblem(nameDraft);
  const descTooLong = [...descDraft.trim()].length > 500;
  async function saveName() {
    setSaving("name");
    setSaved(null);
    setGroupError(null);
    try {
      adopt(await api.patch("/rooms/{roomId}", { path: { roomId }, body: namePatch }));
      setSaved("name");
    } catch (e) {
      const fields: Record<string, string> = {};
      if (isApiError(e)) for (const x of e.problem.errors ?? []) fields[x.field] = x.message;
      setGroupError({ group: "name", text: renameErrorText(e), fields });
    } finally {
      setSaving(null);
    }
  }
  const nameHelp = `${base}-name-help`;
  const descHelp = `${base}-desc-help`;

  const num = (s: string) => (s.trim() === "" ? null : Number(s));
  const fieldErr = (group: GroupKey, field: string) => (groupError?.group === group ? groupError.fields[field] : undefined);
  const saveProps = { canEdit, roHint, saving, saved, groupError };
  return (
    <div className="rd-page" data-testid="rd-settings" data-room-id={room.id}>
      <div className="rd-page__head">
        <Link href={`/rooms/${roomId}`} className="small">← {COMMON.back_to_room}</Link>
        <h1 className="rd-page__title">{SETTINGS.title}</h1>
        <p className="rd-section__note">{room.name}</p>
        <p className="rd-section__note">{SETTINGS.desc}</p>
        {!canEdit && <DisabledHint id={roHint}>{SETTINGS.read_only}</DisabledHint>}
        {room.status === "archived" && <p className="notice notice--info" data-testid="rd-settings-archived">{SETTINGS.lifecycle.archived_now}</p>}
      </div>

      <Group base={base} id="name" title={ROOM_RENAME.group_title} impact={ROOM_RENAME.group_impact}>
        <div className="rd-fields">
          <label className="rd-field">
            <span className="rd-field__label">{ROOM_RENAME.input_label}</span>
            <input
              className="input"
              value={nameDraft}
              disabled={!canEdit}
              onChange={(e) => setNameDraft(e.target.value)}
              aria-invalid={!!nameProblem || !!fieldErr("name", "name") || undefined}
              aria-describedby={nameHelp}
              data-testid="rd-settings-name"
            />
            <span id={nameHelp} className={nameProblem || fieldErr("name", "name") ? "rd-err" : "rd-hint"} data-testid="rd-settings-name-help">
              {fieldErr("name", "name") ?? nameProblem ?? ROOM_RENAME.help}
            </span>
          </label>
          <label className="rd-field">
            <span className="rd-field__label">{ROOM_RENAME.description_label}</span>
            <input
              className="input"
              value={descDraft}
              disabled={!canEdit}
              onChange={(e) => setDescDraft(e.target.value)}
              aria-invalid={descTooLong || !!fieldErr("name", "description") || undefined}
              aria-describedby={descHelp}
              data-testid="rd-settings-description"
            />
            {(descTooLong || fieldErr("name", "description")) && (
              <span id={descHelp} className="rd-err">{fieldErr("name", "description") ?? ROOM_RENAME.description_help}</span>
            )}
          </label>
        </div>
        <SaveRow {...saveProps} group="name" disabled={Object.keys(namePatch).length === 0 || !!nameProblem || descTooLong} onSave={() => void saveName()} />
      </Group>

      <Group base={base} id="visibility" title={SETTINGS.visibility.title} impact={SETTINGS.visibility.impact}>
        {(["workspace", "invited"] as const).map((v) => (
          <label key={v} className="rd-choice" aria-disabled={!canEdit || undefined}>
            <input type="radio" name={`${base}-vis`} checked={vis === v} disabled={!canEdit} onChange={() => setVis(v)} data-testid={`rd-settings-vis-${v}`} />
            <span className="rd-choice__text">
              <span className="rd-choice__label">{SETTINGS.visibility[v]}</span>
              <span className="rd-choice__note">{v === "workspace" ? SETTINGS.visibility.workspace_note : SETTINGS.visibility.invited_note}</span>
            </span>
          </label>
        ))}
        {vis === "invited" && room.visibility !== "invited" && losing > 0 && (
          <p className="rd-warn" role="status" data-testid="rd-settings-vis-losing">
            <Slot text={SETTINGS.visibility.losing} n={losing} />
          </p>
        )}
        <SaveRow {...saveProps} group="visibility" disabled={vis === room.visibility} onSave={() => void save("visibility", { visibility: vis })} />
      </Group>

      <Group base={base} id="runtime" title={SETTINGS.runtime.title} impact={SETTINGS.runtime.impact}>
        {pinned ? (
          <>
            <dl className="rd-kv">
              <dt>{SETTINGS.runtime.computer}</dt>
              <dd data-testid="rd-settings-runtime-name">{pinnedRuntime?.name ?? room.runtime_id}</dd>
              <dt>{SETTINGS.runtime.isolation}</dt>
              <dd>{room.isolation?.kind === "worktree" ? SETTINGS.runtime.worktree : SETTINGS.runtime.none}{room.isolation?.repo_path ? ` · ${room.isolation.repo_path}` : ""}</dd>
            </dl>
            <p className="rd-hint" data-testid="rd-settings-pinned">
              {SETTINGS.runtime.pinned(pinnedRuntime?.name ?? "")}{" "}
              <Link href={`/runtimes/${room.runtime_id}/rebind?room=${room.id}`} data-testid="rd-settings-rebind">{SETTINGS.runtime.rebind}</Link>
            </p>
          </>
        ) : runtimes && runtimes.length === 0 ? (
          <p className="rd-hint" data-testid="rd-settings-no-computer">
            {SETTINGS.runtime.no_computer} · <Link href="/runtimes/new">{SETTINGS.runtime.no_computer_link}</Link>
          </p>
        ) : (
          <>
            <label className="rd-field">
              <span className="rd-field__label">{SETTINGS.runtime.computer}</span>
              <select className="select" value={runtimeId} disabled={!canEdit} onChange={(e) => (setRuntimeId(e.target.value), setRepo(""))} data-testid="rd-settings-runtime">
                <option value="">{SETTINGS.runtime.first_run}</option>
                {(runtimes ?? []).map((r) => (
                  <option key={r.id} value={r.id}>{r.name}</option>
                ))}
              </select>
            </label>
            <span className="rd-field__label">{SETTINGS.runtime.isolation}</span>
            {(["none", "worktree"] as const).map((k) => (
              <label key={k} className="rd-choice" aria-disabled={!canEdit || undefined}>
                <input type="radio" name={`${base}-iso`} checked={isoKind === k} disabled={!canEdit} onChange={() => setIsoKind(k)} data-testid={`rd-settings-iso-${k}`} />
                <span className="rd-choice__text">
                  <span className="rd-choice__label">{SETTINGS.runtime[k]}</span>
                  <span className="rd-choice__note">{k === "none" ? SETTINGS.runtime.none_note : SETTINGS.runtime.worktree_note}</span>
                </span>
              </label>
            ))}
            <label className="rd-choice" aria-disabled="true">
              <input type="radio" disabled />
              <span className="rd-choice__text">
                <span className="rd-choice__label">
                  {SETTINGS.runtime.container}
                  <span className="rd-v11">{AUTONOMY_TEXT.next_version}</span>
                </span>
              </span>
            </label>
            {isoKind === "worktree" &&
              (!chosenRuntime ? (
                <p className="rd-hint" data-testid="rd-settings-repo-needs-computer">{SETTINGS.runtime.repo_needs_computer}</p>
              ) : chosenRuntime.repos.length === 0 ? (
                <p className="rd-hint">{SETTINGS.runtime.repo_none}</p>
              ) : (
                <label className="rd-field">
                  <span className="rd-field__label">{SETTINGS.runtime.repo}</span>
                  <select className="select" value={repo} disabled={!canEdit} onChange={(e) => setRepo(e.target.value)} data-testid="rd-settings-repo">
                    <option value="">{SETTINGS.runtime.repo_placeholder}</option>
                    {chosenRuntime.repos.map((r) => (
                      <option key={r.path} value={r.path}>{r.path}</option>
                    ))}
                  </select>
                </label>
              ))}
            {fieldErr("runtime", "isolation/repo_path") && <p className="rd-err" role="alert">{fieldErr("runtime", "isolation/repo_path")}</p>}
            <SaveRow
              {...saveProps}
              group="runtime"
              onSave={() => void save("runtime", { runtime_id: runtimeId || null, isolation: isoKind === "worktree" ? { kind: "worktree", repo_path: repo } : { kind: "none" } })}
            />
          </>
        )}
      </Group>

      <Group base={base} id="limits" title={SETTINGS.limits.title} impact={SETTINGS.limits.impact}>
        <p className="rd-section__note">{SETTINGS.limits.rule} {SETTINGS.limits.smaller_wins}</p>
        <div className="rd-fields">
          <label className="rd-field">
            <span className="rd-field__label">{SETTINGS.limits.budget}</span>
            <input className="input" inputMode="decimal" value={budget} placeholder={SETTINGS.limits.budget_none} disabled={!canEdit} onChange={(e) => setBudget(e.target.value)} data-testid="rd-settings-budget" />
            {fieldErr("limits", "limits/budget_usd") && <span className="rd-err">{fieldErr("limits", "limits/budget_usd")}</span>}
          </label>
          <label className="rd-field">
            <span className="rd-field__label">{SETTINGS.limits.time}</span>
            <input className="input" value={time} placeholder={SETTINGS.limits.time_placeholder} disabled={!canEdit} onChange={(e) => setTime(e.target.value)} data-testid="rd-settings-time" />
          </label>
          <label className="rd-field">
            <span className="rd-field__label">{SETTINGS.limits.works}</span>
            <input className="input" type="number" min={1} value={works} disabled={!canEdit} onChange={(e) => setWorks(e.target.value)} data-testid="rd-settings-works" />
            {fieldErr("limits", "limits/max_concurrent_works") && <span className="rd-err">{fieldErr("limits", "limits/max_concurrent_works")}</span>}
          </label>
          <label className="rd-field">
            <span className="rd-field__label">{SETTINGS.limits.lanes}</span>
            <input className="input" type="number" min={1} value={lanes} disabled={!canEdit} onChange={(e) => setLanes(e.target.value)} data-testid="rd-settings-lanes" />
            {fieldErr("limits", "limits/max_parallel_lanes") && <span className="rd-err">{fieldErr("limits", "limits/max_parallel_lanes")}</span>}
          </label>
        </div>
        <SaveRow
          {...saveProps}
          group="limits"
          onSave={() =>
            void save("limits", {
              limits: { budget_usd: num(budget), time_limit: time.trim() || null, max_concurrent_works: Number(works) || 0, max_parallel_lanes: Number(lanes) || 0 },
            })
          }
        />
      </Group>

      <Group base={base} id="autonomy" title={SETTINGS.autonomy.title} impact={SETTINGS.autonomy.impact}>
        {(["guided", "autonomous", "supervised"] as const).map((a) => {
          const v11 = a === "supervised";
          return (
            <label key={a} className="rd-choice" aria-disabled={!canEdit || v11 || undefined}>
              <input type="radio" name={`${base}-aut`} checked={autonomy === a} disabled={!canEdit || v11} onChange={() => setAutonomy(a)} data-testid={`rd-settings-autonomy-${a}`} />
              <span className="rd-choice__text">
                <span className="rd-choice__label">
                  {AUTONOMY_TEXT[a].label}
                  {a === "guided" && ` ${AUTONOMY_TEXT.default_tail}`}
                  {v11 && <span className="rd-v11">{AUTONOMY_TEXT.next_version}</span>}
                </span>
                <span className="rd-choice__note">{AUTONOMY_TEXT[a].note}</span>
              </span>
            </label>
          );
        })}
        <SaveRow {...saveProps} group="autonomy" disabled={autonomy === room.autonomy} onSave={() => void save("autonomy", { autonomy })} />
      </Group>

      <Group base={base} id="director" title={SETTINGS.director.title} impact={SETTINGS.director.impact}>
        <label className="rd-field">
          <span className="rd-field__label">{SETTINGS.director.title}</span>
          <select className="select" value={director} disabled={!canEdit} onChange={(e) => setDirector(e.target.value)} data-testid="rd-settings-director">
            <option value="">{SETTINGS.director.empty}</option>
            {members.map((m) => (
              <option key={m.user.id} value={m.user.id}>{personName(m.user)}</option>
            ))}
          </select>
        </label>
        {!director && <p className="rd-hint" data-testid="rd-settings-director-empty">{SETTINGS.director.empty_note}</p>}
        <SaveRow {...saveProps} group="director" disabled={director === (room.default_director_user_id ?? "")} onSave={() => void save("director", { default_director_user_id: director || null })} />
      </Group>

      <Group base={base} id="owner" title={SETTINGS.owner.title} impact={SETTINGS.owner.impact}>
        <dl className="rd-kv">
          <dt>{SETTINGS.owner.owner}</dt>
          <dd data-testid="rd-settings-owner">{personName(humans.find((p) => p.user!.id === room.owner_user_id)?.user ?? members.find((m) => m.user.id === room.owner_user_id)?.user)}</dd>
        </dl>
        <div className="rd-group__actions">
          <button
            type="button"
            className="btn btn--sm"
            aria-disabled={!gates.head.ok || undefined}
            aria-describedby={!gates.head.ok ? headHint : undefined}
            onClick={() => gates.head.ok && setTransferring(true)}
            data-testid="rd-settings-transfer"
          >
            {SETTINGS.owner.transfer}
          </button>
        </div>
        <label className="rd-field">
          <span className="rd-field__label">{SETTINGS.owner.deputy}</span>
          <select className="select" value={deputy} disabled={!gates.head.ok} onChange={(e) => setDeputy(e.target.value)} data-testid="rd-settings-deputy">
            <option value="">{SETTINGS.owner.deputy_none}</option>
            {humans
              .filter((p) => p.user!.id !== room.owner_user_id)
              .map((p) => (
                <option key={p.user!.id} value={p.user!.id}>{personName(p.user)}</option>
              ))}
          </select>
        </label>
        <p className="rd-hint">{SETTINGS.owner.deputy_hint}</p>
        <div className="rd-group__actions">
          <button
            type="button"
            className="btn btn--sm btn--primary"
            aria-disabled={!gates.head.ok || deputy === (room.deputy_owner_user_id ?? "") || undefined}
            aria-describedby={!gates.head.ok ? headHint : undefined}
            disabled={saving === "deputy"}
            onClick={() => gates.head.ok && deputy !== (room.deputy_owner_user_id ?? "") && void saveDeputy()}
            data-testid="rd-settings-save-deputy"
          >
            {saving === "deputy" ? COMMON.saving : SETTINGS.owner.set_deputy}
          </button>
          {saved === "deputy" && <span className="rd-saved" role="status">{COMMON.saved}</span>}
          {groupError?.group === "deputy" && <p className="rd-err" role="alert">{groupError.text}</p>}
        </div>
        {!gates.head.ok && <DisabledHint id={headHint}>{SETTINGS.head_only}</DisabledHint>}
      </Group>

      <Group base={base} id="links" title={SETTINGS.links.title} impact={SETTINGS.links.impact}>
        {links && links.length === 0 ? (
          <p className="rd-hint" data-testid="rd-settings-links-none">{SETTINGS.links.none}</p>
        ) : (
          <ul className="rd-list" data-testid="rd-settings-links">
            {(links ?? []).map((l) => (
              <li key={l.id} className="rd-row">
                <span className="rd-row__name">{l.target_room.name}</span>
              </li>
            ))}
          </ul>
        )}
        <div className="rd-group__actions">
          <Link href={`/rooms/${roomId}/settings/links`} className="btn btn--sm" data-testid="rd-settings-links-manage">{SETTINGS.links.manage}</Link>
          <Link href={`/rooms/${roomId}/reads`} className="btn btn--sm btn--ghost" data-testid="rd-settings-reads">{SETTINGS.reads_link}</Link>
        </div>
      </Group>

      <Group base={base} id="lifecycle" title={SETTINGS.lifecycle.title} impact={SETTINGS.lifecycle.impact}>
        <div className="rd-group__actions">
          {room.status === "archived" ? (
            <button
              type="button"
              className="btn btn--sm"
              aria-disabled={!gates.archive.ok || undefined}
              aria-describedby={!gates.archive.ok ? `${base}-arch` : undefined}
              onClick={() => gates.archive.ok && void unarchive()}
              data-testid="rd-settings-unarchive"
            >
              {SETTINGS.lifecycle.unarchive}
            </button>
          ) : (
            <button
              type="button"
              className="btn btn--sm"
              aria-disabled={!gates.archive.ok || undefined}
              aria-describedby={!gates.archive.ok ? `${base}-arch` : undefined}
              onClick={() => gates.archive.ok && setArchiving(true)}
              data-testid="rd-settings-archive"
            >
              {SETTINGS.lifecycle.archive}
            </button>
          )}
          <button
            type="button"
            className="btn btn--sm confirm-dlg__danger"
            aria-disabled={!deleteOk || undefined}
            aria-describedby={!deleteOk ? `${base}-del` : undefined}
            onClick={() => deleteOk && setDeleting(true)}
            data-testid="rd-settings-delete"
          >
            {SETTINGS.lifecycle.delete}
          </button>
        </div>
        {!gates.archive.ok && <DisabledHint id={`${base}-arch`}>{gates.archive.reason}</DisabledHint>}
        {!deleteOk && (
          <DisabledHint id={`${base}-del`}>
            {!gates.delete.ok ? gates.delete.reason : !delGate.ok && delGate.count !== undefined ? <Slot text={delGate.reason} n={delGate.count} /> : null}
          </DisabledHint>
        )}
        {unarchiveError && <p className="rd-err" role="alert">{unarchiveError}</p>}
      </Group>

      {transferring && (
        <TransferOwnerDialog
          roomId={roomId}
          candidates={humans.filter((p) => p.user!.id !== room.owner_user_id)}
          onDone={(r) => (adopt(r), setTransferring(false), void load())}
          onClose={() => setTransferring(false)}
        />
      )}
      {archiving && <ArchiveRoomDialog room={room} onArchived={(r) => (adopt({ ...room, ...r }), void load())} onClose={() => setArchiving(false)} />}
      {deleting && <DeleteRoomDialog room={room} onDeleted={() => onDeleted?.({ id: room.id, name: room.name })} onClose={() => setDeleting(false)} />}
    </div>
  );
}

interface SaveRowProps {
  group: GroupKey;
  onSave: () => void;
  disabled?: boolean;
  canEdit: boolean;
  roHint: string;
  saving: GroupKey | null;
  saved: GroupKey | null;
  groupError: { group: GroupKey; text: string; fields: Record<string, string> } | null;
}
/** 묶음의 「저장」 줄 — 권한 밖이면 비활성 + 머리의 읽기 전용 사유를 가리킨다. 필드 오류는 필드 옆에, 나머지는 여기에. */
function SaveRow({ group, onSave, disabled, canEdit, roHint, saving, saved, groupError }: SaveRowProps) {
  return (
    <div className="rd-group__actions">
      <button
        type="button"
        className="btn btn--sm btn--primary"
        aria-disabled={!canEdit || disabled || undefined}
        aria-describedby={!canEdit ? roHint : undefined}
        disabled={saving === group}
        onClick={() => canEdit && !disabled && onSave()}
        data-testid={`rd-settings-save-${group}`}
      >
        {saving === group ? COMMON.saving : COMMON.save}
      </button>
      {saved === group && <span className="rd-saved" role="status">{COMMON.saved}</span>}
      {groupError?.group === group && !Object.keys(groupError.fields).length && <p className="rd-err" role="alert" data-testid={`rd-settings-error-${group}`}>{groupError.text}</p>}
    </div>
  );
}
/** 묶음 하나 — 제목 + 「바꿨을 때의 영향」 한 줄(§4.11). */
function Group({ base, id, title, impact, children }: { base: string; id: string; title: string; impact: string; children: React.ReactNode }) {
  return (
    <section className="rd-group" aria-labelledby={`${base}-${id}`} data-testid={`rd-settings-group-${id}`}>
      <div className="rd-group__head">
        <h2 className="rd-group__title" id={`${base}-${id}`}>{title}</h2>
        <p className="rd-group__impact" data-testid={`rd-settings-group-${id}-impact`}>{impact}</p>
      </div>
      {children}
    </section>
  );
}

/** 방장 넘기기 확인(§4.11 「방장 넘기기는 확인 다이얼로그 + 시스템 메시지」) — 새 방장은 이 방의 사람 참여자(서버 422 not_participant). */
export function TransferOwnerDialog({ roomId, candidates, onDone, onClose }: { roomId: string; candidates: RoomParticipant[]; onDone: (r: Room) => void; onClose: () => void }) {
  const [pick, setPick] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function confirm() {
    if (!pick) return;
    setBusy(true);
    setError(null);
    try {
      onDone(await api.put("/rooms/{roomId}/owner", { path: { roomId }, body: { user_id: pick } }));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <ConfirmDialog
      title={TRANSFER_DIALOG.title}
      confirmLabel={TRANSFER_DIALOG.confirm}
      busyLabel={TRANSFER_DIALOG.busy}
      cancelLabel={COMMON.cancel}
      busy={busy}
      error={error}
      onConfirm={() => void confirm()}
      onClose={onClose}
      testId="rd-transfer-dialog"
    >
      {candidates.length === 0 ? (
        <p className="confirm-dlg__p" data-testid="rd-transfer-none">{TRANSFER_DIALOG.none}</p>
      ) : (
        <label className="rd-field">
          <span className="rd-field__label">{TRANSFER_DIALOG.pick}</span>
          <select className="select" value={pick} onChange={(e) => setPick(e.target.value)} data-testid="rd-transfer-pick">
            <option value="">{TRANSFER_DIALOG.pick_placeholder}</option>
            {candidates.map((p) => (
              <option key={p.user!.id} value={p.user!.id}>{personName(p.user)}</option>
            ))}
          </select>
        </label>
      )}
      <p className="confirm-dlg__p">{TRANSFER_DIALOG.body}</p>
    </ConfirmDialog>
  );
}

export default RoomSettingsForm;
