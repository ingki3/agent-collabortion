"use client";
/**
 * S18 방 만들기(SCREEN §4.5, T-R2-W1) — **칸 하나다**(FR-2.1). 설계 목표는 "빨리 만들기"가 아니라 **"고르지 않은 것이 무엇인지 알려 주고
 * 빨리 만들기"**(FR-2.1.1):
 *   - 이름(필수·자동 포커스) · 한 줄 설명(선택).
 *   - ⓘ 한 줄 — 워크스페이스 기본값(`room_defaults` → `default_isolation`)을 읽어 문장을 만든다. 고정하지 않는다(설정을 바꾼 워크스페이스에서
 *     거짓말이 된다). 「방 설정」 링크는 만들기 **전에는 비활성** + 「만든 뒤 바꿀 수 있습니다」 — 만들기 전에 설정을 편집하게 하면 그것이 마법사다.
 *   - 같은 이름이 있으면 **막지 않고** 경고 한 줄(작업 폴더 브랜치 이름이 `colab/<방 slug>/…` 라 헷갈린다) — 판정은 `listRooms?q=`(별도 op 없음).
 *   - 연결된 컴퓨터가 0개여도 만든다(`createRoom` 은 `409 no_runtime` 이 없다).
 * 「만들기」 → 방 생성 → `/rooms/<id>`. Esc·「취소」 → 아무것도 만들지 않는다.
 */
import { useEffect, useId, useRef, useState } from "react";
import { DisabledHint } from "./PageHead";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { CREATE_ROOM, roomDefaultsLine } from "@/lib/wording";
import { pageItems, type Room, type RoomListItem, type WorkspaceSettings } from "@/lib/api/types";
import "./confirm-dialog.css";
import "./room-card.css";

export interface CreateRoomDialogProps {
  workspaceId: string;
  onCreated: (room: Room) => void;
  onClose: () => void;
}

const sameName = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

export function CreateRoomDialog({ workspaceId, onCreated, onClose }: CreateRoomDialogProps) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [settings, setSettings] = useState<WorkspaceSettings | null>(null);
  const [duplicate, setDuplicate] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  // 한 번 연 다이얼로그의 재시도는 같은 요청이다 — 두 번 눌러도 방이 둘 생기지 않게(createRoom 의 Idempotency-Key).
  const idem = useRef(newIdempotencyKey());
  const nameRef = useRef<HTMLInputElement>(null);
  const base = useId();
  const titleId = `${base}-title`;
  const createHint = `${base}-create-hint`;
  const settingsHint = `${base}-settings-hint`;

  useEffect(() => {
    nameRef.current?.focus();
  }, []);
  useEffect(() => {
    // 설정을 못 읽어도 만들기는 막지 않는다 — ⓘ 는 계약 기본값(격리 없음)으로 말한다.
    api.get("/workspaces/{workspaceId}/settings", { path: { workspaceId } }).then(setSettings, () => setSettings(null));
  }, [workspaceId]);
  useEffect(() => {
    const n = name.trim();
    if (!n) {
      setDuplicate(false);
      return;
    }
    let live = true;
    const t = setTimeout(() => {
      api
        .get("/workspaces/{workspaceId}/rooms", { path: { workspaceId }, query: { q: n, participating: false, include_archived: true } })
        .then((page) => live && setDuplicate(pageItems<RoomListItem>(page).some((r) => sameName(r.name, n))), () => live && setDuplicate(false));
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [name, workspaceId]);

  const empty = !name.trim();
  async function create() {
    if (empty || busy) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const room = await api.post("/workspaces/{workspaceId}/rooms", {
        path: { workspaceId },
        body: { name: name.trim(), description: description.trim() },
        idempotencyKey: idem.current,
      });
      onCreated(room);
    } catch (e) {
      if (isApiError(e) && e.problem.errors?.length) {
        setFieldErrors(Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message])));
      }
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="confirm-dlg__scrim" role="presentation" onClick={(e) => e.target === e.currentTarget && !busy && onClose()} onKeyDown={(e) => e.key === "Escape" && !busy && onClose()}>
      <form
        className="confirm-dlg create-room"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onSubmit={(e) => {
          e.preventDefault();
          void create();
        }}
        data-testid="create-room-dialog"
      >
        <h2 className="confirm-dlg__title" id={titleId}>{CREATE_ROOM.title}</h2>
        <label className="create-room__field">
          <span className="create-room__label">{CREATE_ROOM.name}</span>
          <input
            ref={nameRef}
            className="input"
            value={name}
            maxLength={200}
            required
            placeholder={CREATE_ROOM.name_placeholder}
            aria-invalid={!!fieldErrors.name || undefined}
            onChange={(e) => setName(e.target.value)}
            data-testid="create-room-name"
          />
          {fieldErrors.name && <span className="create-room__err">{fieldErrors.name}</span>}
        </label>
        <label className="create-room__field">
          <span className="create-room__label">{CREATE_ROOM.description}</span>
          <input
            className="input"
            value={description}
            maxLength={500}
            placeholder={CREATE_ROOM.description_placeholder}
            aria-invalid={!!fieldErrors.description || undefined}
            onChange={(e) => setDescription(e.target.value)}
            data-testid="create-room-description"
          />
          {fieldErrors.description && <span className="create-room__err">{fieldErrors.description}</span>}
        </label>
        <div className="create-room__info" data-testid="create-room-info">
          <p className="confirm-dlg__p">
            <span aria-hidden="true">ⓘ </span>
            <span data-testid="create-room-defaults">{roomDefaultsLine(settings)}</span> — {CREATE_ROOM.info_tail}
          </p>
          <button type="button" className="btn btn--sm" aria-disabled="true" aria-describedby={settingsHint} onClick={(e) => e.preventDefault()} data-testid="create-room-settings">
            {CREATE_ROOM.settings_link}
          </button>
          <DisabledHint id={settingsHint}>{CREATE_ROOM.settings_later}</DisabledHint>
        </div>
        {duplicate && (
          <p className="create-room__warn" role="status" data-testid="create-room-duplicate">
            {CREATE_ROOM.duplicate}
          </p>
        )}
        {error && !Object.keys(fieldErrors).length && <p className="problem confirm-dlg__problem" role="alert" data-testid="create-room-error">{error}</p>}
        <div className="confirm-dlg__actions">
          <button type="button" className="btn" disabled={busy} onClick={onClose} data-testid="create-room-cancel">
            {CREATE_ROOM.cancel}
          </button>
          <button
            type="submit"
            className="btn btn--primary"
            aria-disabled={empty || undefined}
            aria-describedby={empty ? createHint : undefined}
            disabled={busy}
            data-testid="create-room-submit"
          >
            {busy ? CREATE_ROOM.busy : CREATE_ROOM.create}
          </button>
        </div>
        {empty && <DisabledHint id={createHint}>{CREATE_ROOM.name_required}</DisabledHint>}
      </form>
    </div>
  );
}

export default CreateRoomDialog;
