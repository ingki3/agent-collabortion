# MISSION_FOLDERS — 작업 폴더를 방 → 미션 → 에이전트로 (T-FOLDERS 1단계: 설계)

| 항목 | 내용 |
|---|---|
| 상태 | **Director 승인 2026-09-26(D1~D8) — 구현 완료(2단계, 같은 브랜치 `design/mission-folders`).** 판정은 §8, 계약은 daemon-protocol v0.10.0 · harness v0.9.7 · openapi v0.3.4(PR #350, dev 머지) |
| 근거 | Director 방향 2026-09-25 · 게임 제작 방 실측(아래 §0) · 현재 코드(dev `origin/dev`) |
| 함께 바뀐 문서 | `PRD.md` FR-6.1·FR-6.4·§8.4 턴 프롬프트·용어표 · `SCREEN.md` §4.16 S13 — 전부 `[FOLDERS]` 표식, 「Lead 승인 전」 |
| 계약 | 제안은 §6, **확정 문장은 계약 파일이 정본**이다 — 이 문서와 다르면 계약을 따른다 |

---

## 0. 왜 — 실측과 현재 코드

**실측(게임 제작 방, 격리 `none`)**: lane 마다 폴더가 따로라 코드 협업 미션에서 Developer 가 `work/index.html` 을 못 찾았다. Writer·Designer 는 Lead lane 폴더의 **절대 경로를 직접 grep** 해 우회했다 — FR-6.1 「크로스 lane 읽기는 아티팩트로만」을 규칙이 아니라 **에이전트가 비켜 가는 장애물**로 만든 셈이다. 규칙이 막은 것은 협업이고, 격리는 지키지 못했다(경로만 알면 읽힌다 — OS 수준 차단이 없다).

**현재 배치(코드 대조)**

| 격리 | 경로 | 누가 짓나 | 근거 |
|---|---|---|---|
| `none` | `<root>/sessions/<room_id>/<lane_id>/` | **데몬** (첫 attempt 는 id 없음 — T-S21 Lead 결정 A) | `daemon/internal/workdir/workdir.go:33` `Path`, `:227` |
| `worktree` | `<root>/worktrees/<slug>/<agent-slug>/`, 브랜치 `colab/<slug>/<agent-slug>` | **서버** | `server/internal/workdirs/gc.go:147` `PlanWorktree`, `queue/bundle.go:455` |
| 테스트 채팅 | `<root>/.colab/testchat/<test_chat_id>/` | 서버 | daemon-protocol §4.5 |

**발견 — PRD 와 코드가 이미 어긋난다(FINDING-1).** `queue/bundle.go:52` 가 slug 재료로 `COALESCE(wk.title, s.name)` — **미션 제목**을 쓴다. PRD FR-6.4 는 `colab/<room>/<agent>` 인데 실제로는 그 에이전트의 **첫 미션 제목**으로 워크트리 폴더·브랜치가 만들어지고(`ExistingForAgent` 가 이후 재사용), 미션 밖 첫 턴이면 방 이름이 된다. 이 설계가 경로 규칙을 다시 정하므로 2단계에서 같이 바로잡는다(§3 c).

---

## 1. 결론 요약 (권고안)

```
<workdir_root>/rooms/<room>/
    <mission>/                       ← 미션 하나 (none·container)
        _shared/                     ← 미션 공용 (그 미션 참여 에이전트 전부 읽기·쓰기)
        <agent>/                     ← 에이전트 cwd (자기만 쓰기, 같은 미션 형제는 읽기)
    _room/<agent>/                   ← 미션 밖 턴
    _worktrees/<agent>/              ← worktree 격리 체크아웃 (방×에이전트, 브랜치 colab/<room>/<agent>)
<workdir_root>/.colab/testchat/<id>/ ← 테스트 채팅 (그대로)
<workdir_root>/sessions/…            ← 옛 배치 (옮기지 않는다, 기존 규칙으로 GC)
<workdir_root>/worktrees/…           ← 옛 배치 (옮기지 않는다)
```

- 이름 조각 = `<슬러그>-<id 앞 8자리>` 를 **만들 때 한 번 정해 행에 저장**(이름이 바뀌어도 폴더는 안 옮긴다).
- 경로는 **`none` 도 서버가 짓는다** — worktree 와 같은 규칙(§4.1 v0.7.3). 첫 attempt 부터 `workdir.id` 가 실린다.
- 같은 미션 안: **형제 폴더 읽기 허용, 쓰기는 자기 폴더 + `_shared`**. 경로는 **턴 프롬프트** `<folders>` 블록으로 알린다(브리프 [1]~[5] 바이트 동일 유지).
- FR-6.1 「크로스 lane 은 아티팩트로만」 → 「**같은 미션 안은 폴더로, 미션·방 경계를 넘을 때는 아티팩트로**」.
- GC: `none` 미션 폴더는 **미션이 닫히면 `<mission>/` 통째로** 삭제(지금 규칙 유지, 단위만 lane → 미션). `worktree` 는 변화 없음.

---

## 2. 결정 필요 항목 — 선택지와 권고 (Lead 판정 요청)

| # | 질문 | 선택지 | 권고 |
|---|---|---|---|
| **D1** | 경로 조각: 이름이냐 id 냐 | A `<slug>-<id8>` 만들 때 고정 · B id 만(uuid) · C 이름만(바뀌면 이동) | **A** — 에이전트·사람이 `ls` 로 알아보고, 이름 바꾸기(FR-2.1.2)에도 경로가 안 흔들린다. C 는 실행 중 lane 의 cwd·런타임 세션(`session/load {cwd}`)을 깬다 |
| **D2** | 같은 미션 형제 폴더 | A 읽기 허용·쓰기 자기+`_shared` · B 전부 읽기·쓰기 · C 지금처럼 아티팩트만 | **A** — 실측의 우회를 정식 경로로. B 는 동시 쓰기 충돌(병렬 lane). C 는 실측이 이미 깨진 것을 보였다 |
| **D3** | 같은 에이전트의 여러 lane(같은 미션, 시나리오 A Researcher 3-way) | A 에이전트 폴더 하나 공유·병렬 유지 · B 에이전트 폴더 공유·같은 미션 안에선 순차(worktree 처럼) · C `<agent>/<lane-short>/` 하위 폴더 | **A** + 턴 프롬프트에 「형제 lane 이 같은 폴더에서 동시에 일한다 — 새 파일 이름에 갈래 표지(`<lane_label>`)를 넣어라」. B 는 목표 4(병렬)를 깬다. C 는 지금 문제(자기 것도 못 찾음)를 에이전트 안에 재생산 |
| **D4** | 미션 공용 `_shared` | A 둔다(미션 참여자 전부 쓰기) · B 안 둔다(Lead 폴더가 사실상 공용) | **A** — 「누구 폴더에 둘까」 로 에이전트가 헤매지 않게. 최종 산출물은 여전히 `artifact submit`(§8.3 규약 그대로) |
| **D5** | 쓰기 제한의 강제 | A 규약(브리프·턴 프롬프트)만 · B + claude_code `permissions.deny` 로 형제 폴더 `Edit/Write` 거부 · C OS 샌드박스 | **A 로 시작, B 를 2단계 후보**. hermes 는 deny 수단이 없어 B 는 런타임별 비대칭 — 강제가 반쪽이면 규약보다 약속이 커 보인다. C 는 v1 범위 밖 |
| **D6** | 옛 `sessions/<room>/<lane>/` 폴더 | A 옮기지 않음(진행 중 lane 은 옛 경로로 계속, 새 lane 부터 새 배치) · B 일괄 이동 · C 심링크 | **A** — 재진입 lane 은 `lane.workdir_id` 의 저장 경로를 그대로 쓰고(지금 코드), 런타임 resume 의 `cwd` 가 바뀌지 않는다. 옛 폴더는 기존 GC(미션 닫힘·`last_used_at+14일`)로 자연 소멸. B 는 실행 중 cwd 를 깬다 |
| **D7** | `worktree` 방과 미션 폴더 | A 체크아웃은 방×에이전트 그대로(`_worktrees/<agent>`), 미션 폴더 안 만듦 · B 미션×에이전트 워크트리(브랜치 `colab/<room>/<mission>/<agent>`) · C 체크아웃 + 미션 `_shared` 만 추가 | **C** — 코드 연속성(V19-B: 방이 사는 동안 브랜치 하나, 미션 2 가 미션 1 의 미병합 커밋 위에서 일한다)을 지키면서, 코드가 아닌 공유물(스펙 메모·스크린샷)의 자리를 준다. B 는 미병합 커밋이 다음 미션에서 사라지고 재바인딩 diff 순서(FR-9.2)를 다시 연다 |
| **D8** | 미션 닫힘 시 `none` 폴더 | A 즉시 삭제(지금) · B `last_used_at + retention` | **A** — 다음 미션으로 넘길 것은 아티팩트(FR-4.3). 단 S13·닫기 확인에 「이 미션의 작업 폴더 N개(〈용량〉)가 정리됩니다」를 적는다(§5) |

---

## 3. 설계 상세

### a. 경로 규칙

| 자리 | 조각 | 예 |
|---|---|---|
| 방 | `Slug(room.name)-<room_id[:8]>` | `game-studio-3f2a91c0` |
| 미션 | `Slug(work.title)-<work_id[:8]>` | `snake-prototype-8b11de02` |
| 미션 밖 | `_room` (예약어 — `Slug` 는 선행 `_` 를 만들지 않으므로 충돌 없음) | |
| 에이전트 | `Slug(agent.name)-<agent_id[:8]>` | `developer-0c7e5d19` |
| 공용 | `_shared` | |

- `Slug` 는 `workdirs.Slug`(gc.go:183) 재사용 — 한글 이름은 전부 `-` 로 떨어져 빈 문자열이면 `x`. 그래서 **id8 이 판별자**이고 슬러그는 보조 표지다. 한글 방 이름이 많은 실사용에서 슬러그가 `x` 가 되는 것이 흔하다 → **대안: 한글 보존 슬러그**(파일시스템은 UTF-8 허용, git 브랜치도 허용). 권고는 경로는 한글 보존, **브랜치는 지금 `Slug`**(ref 호환). 이 부분은 D1 의 하위 결정으로 Lead 판정.
- id8 충돌: 같은 런타임에서 32비트 접두 충돌은 무시 가능하나, 서버가 경로를 지을 때 같은 `runtime_id` 의 활성 `workdir` 행과 부딪히면 12자리로 늘린다(결정적).
- **만들 때 고정**: 경로는 `workdir.path_or_ref` 에 저장되고 이후 어떤 이름 변경도 경로를 다시 짓지 않는다. 화면은 경로 대신 DB 의 현재 이름을 보여 준다(§5).
- **여러 lane 의 에이전트**: D3 A — 같은 미션의 같은 에이전트 lane 들은 한 `workdir` 행을 공유(`lane.workdir_id` 가 같은 행). 다른 미션의 lane 은 다른 폴더.
- **미션 밖 턴**: `_room/<agent>/`. 방이 사는 동안 하나. GC 는 `last_used_at + workdir_retention_days`(지금 규칙 그대로).
- **테스트 채팅**: 변경 없음(`.colab/testchat/<id>` — 방이 없다).
- **이관**: D6 A. 판정 방법 — `lane.workdir_id` 가 가리키는 행이 있으면 그 경로(옛 배치 포함)를 그대로 쓰고, 새 행을 만들 때만 새 규칙. 데몬 `List`(workdir.go:350)는 `sessions/`·`worktrees/`·`rooms/` 셋을 모두 훑는다(S13·GC 가 옛 폴더를 놓치지 않게).

### b. 공유 규칙

| 대상 | 읽기 | 쓰기 |
|---|---|---|
| 자기 `<mission>/<agent>/` | ✅ | ✅ |
| 같은 미션 `_shared/` | ✅ | ✅ |
| 같은 미션 형제 `<agent>/` | ✅ | ❌ (규약) |
| 다른 미션 · `_room` · 다른 방 | ❌ → 아티팩트, 다른 방은 S23 `room read` | ❌ |
| `worktree` 형제 체크아웃 | ✅ 참고용 | ❌ (E13-08: 두 에이전트가 한 트리 = 저장소 손상) |

**알리는 법 — 턴 프롬프트 `<folders>` 블록(브리프가 아니다).** 브리프 [1]~[5] 는 바이트 동일(E12-11, 캐시)이고 미션·형제 구성은 턴마다 바뀐다(참여자 추가, 미션 밖 턴). 그래서 `<roster_status>`(harness v0.9.2)와 같은 층에 둔다.

```
<folders>
you:     /…/rooms/game-studio-3f2a91c0/snake-prototype-8b11de02/developer-0c7e5d19   (your working folder — write here)
shared:  /…/rooms/game-studio-3f2a91c0/snake-prototype-8b11de02/_shared            (everyone on this mission reads and writes)
lead:    /…/snake-prototype-8b11de02/lead-51aa02e3     (read only)
writer:  /…/snake-prototype-8b11de02/writer-77c0b3a4   (read only)
Other missions and rooms are not in these folders: ask for an artifact, or use `colab room read`.
</folders>
```

- 경로는 **서버가 안다**(§4 e — 서버가 짓는다). 목록에는 **폴더가 실제로 만들어진 형제만**(그 미션의 `workdir` 행이 있는 것) — 없는 경로를 주면 에이전트가 헛돈다.
- 브리프 [2] 에는 **고정 문장**만 한 줄: 「턴 프롬프트의 `<folders>` 가 네 폴더와 같은 미션 동료의 폴더다. 쓰기는 네 폴더와 shared 에만」.
- **FR-6.1 고침**: 「크로스 lane 읽기는 아티팩트로만」 → 「**같은 미션 안에서는 폴더로 읽는다(쓰기는 자기 폴더와 `_shared`). 미션·방 경계를 넘을 때와 리뷰·병합 대상은 아티팩트다.**」 리뷰 대상이 diff 아티팩트라는 FR-4.3·시나리오 B 는 그대로 — 폴더 읽기는 참고, 판정 근거는 제출물.

### c. worktree 격리와의 관계 (D7 C)

- 체크아웃: `rooms/<room>/_worktrees/<agent>/`, 브랜치 `colab/<room-slug>/<agent-slug>` — **PRD FR-6.4 가 이미 적은 방×에이전트**. FINDING-1(미션 제목 slug)을 이 자리에서 방 이름으로 바로잡는다. 이미 만들어진 체크아웃은 행의 경로를 그대로 쓰므로(ExistingForAgent) 영향 없음 — 새 방·새 에이전트부터.
- 미션 `_shared`: worktree 방에도 `rooms/<room>/<mission>/_shared/` 를 만든다(저장소 밖이라 커밋에 섞이지 않는다). 에이전트 cwd 는 체크아웃.
- `<folders>` 블록: worktree 방이면 `you:` 가 체크아웃, `shared:` 가 미션 공용, 형제는 체크아웃 경로(읽기 전용 표시).

### d. GC · S13 · rebind

| 대상 | 규칙 | 바뀌는 것 |
|---|---|---|
| `none` 미션 폴더 | 미션 닫힘 즉시 — **`<mission>/` 하나를 한 번에** | lane 단위 여러 행 → 미션 단위(에이전트 행 + `_shared` 행). `missionDirSQL`(complete.go:434)이 lane 을 타고 판정하던 것을 `workdir.work_id` 로 직접 |
| `_room/<agent>` | `last_used_at + retention` | 없음 |
| `_shared`(worktree 방) | 미션 닫힘 즉시(저장소 밖이고 커밋이 없다) | 신규 |
| `_worktrees/<agent>` | 지금 규칙(병합·클린 / 커밋 0·클린만 삭제, 브랜치 남김) | 없음 |
| 옛 `sessions/`·`worktrees/` | 지금 규칙 | 없음 |
| 방 삭제(FR-2.6) | 방의 남은 행 gc | 데몬은 gc 뒤 빈 `rooms/<room>/` 를 지운다 |

- **용량 상한**(`workdir_disk_quota_gb`): 변화 없음 — 데몬이 root 전체를 잰다.
- **S13**: §5.
- **rebind(S17)**: `none` 은 새 컴퓨터의 `workdir_root` 로 경로를 다시 짓는다(서버가 짓기 때문에 가능 — 지금은 데몬이 지어 서버가 몰랐다). 폴더 내용은 옮기지 않는다(지금과 같다 — 아티팩트만). `_shared` 도 사라진다는 문장을 S17 유실 경고에 `none` 용으로 추가. `{{COLAB_REBIND_DIR}}` 는 그대로.

---

## 4. 서버·데몬 영향 (2단계 구현 범위 — 승인 뒤)

| 층 | 변경 |
|---|---|
| DB 마이그레이션 | `workdir.work_id uuid NULL REFERENCES work(id) ON DELETE SET NULL` · `workdir.role text` (`agent`/`shared`) · CHECK `agent_id IS NOT NULL OR lane_id IS NOT NULL` 를 `… OR role = 'shared'` 로 완화 · 인덱스 `(session_id, work_id, agent_id)` |
| 서버 `workdirs` | `PlanDir(root, room, work?, agent)` 신설 · `PlanWorktree` 경로를 `rooms/<room>/_worktrees/<agent>` · 슬러그 재료를 방 이름으로(FINDING-1) · `EnsureBundleRow` 가 `dir` 도 (room, work, agent) 로 찾고 없으면 **첫 attempt 에 행 생성** → 모든 번들에 `workdir.id` |
| 서버 `queue` | `none` 도 `workdir_root` 없으면 `errNoWorkdirRoot`(지금 worktree 와 같은 거부) · 턴 프롬프트 `<folders>` 블록 · 브리프 [2] 고정 한 줄 |
| 서버 GC | `gcWorkdirs` 를 `work_id` 기준으로 · `_shared` 행 포함 |
| 데몬 | `Prepare`: path 가 오면 그대로(이미 그렇다), `_shared` 는 서버가 번들에 `shared_path` 로 싣고 데몬이 `mkdir -p` · `List` 가 `rooms/` 트리를 훑는다(`.colab-workdir.json` 표식으로 id 복원 — 이미 있음) · gc 뒤 빈 상위 폴더 정리 |
| 웹 | S13 트리 표시(§5) · 미션 닫기 확인 문장 |
| e2e | 새 번호 1개: 같은 미션 두 에이전트 — B 가 A 폴더 파일을 `<folders>` 경로로 읽는다 · `_shared` 쓰기 · 미션 닫힘 뒤 `<mission>/` 삭제 · 옛 `sessions/` lane 재진입이 옛 경로 유지 |

---

## 5. 화면 (SCREEN S13 초안 — 본문은 `SCREEN.md` §4.16 `[FOLDERS]`)

- 목록을 **방 → 미션 → 에이전트 트리**로 묶는다(평면 표 대신). 미션 줄에 합계 용량·「미션이 닫히면 정리」, 그 아래 `_shared`(「미션 공용」)와 에이전트별 행. 「미션 밖」 묶음, 「옛 배치(세션 폴더)」 묶음.
- 경로 칸은 실제 경로(고정)를 작게, 굵게는 **현재 이름**(방·미션·에이전트). 이름이 바뀌었으면 「폴더 이름은 만들 때의 이름입니다」 툴팁.
- 미션 닫기 확인(S7/S9 쪽 다이얼로그)에 「이 미션의 작업 폴더 N개(〈용량〉)가 정리됩니다 — 남길 것은 아티팩트로 제출하세요」.
- **Pencil 반영 요청**(Lead 가 넣는다 — 이 브랜치는 `.pen` 을 건드리지 않았다):
  - 프레임 **S13 작업 폴더 관리**: 표 → 3단 트리. 행 요소: 들여쓰기 화살표, 종류 배지(`dir`/`worktree`/`공용`), 이름(굵게)+경로(회색 작게), 용량, 마지막 사용, 정리 예정 문구. 그룹 머리 문구 「미션 밖」 「옛 배치(세션 폴더)」.
  - 프레임 **미션 닫기 확인**: 본문 한 줄 추가 「이 미션의 작업 폴더 3개(12 MB)가 정리됩니다 — 남길 것은 아티팩트로 제출하세요」.
  - 프레임 **S17 컴퓨터 바꾸기**: `none` 방 유실 경고 문구 「작업 폴더(미션 공용 포함)는 옮겨지지 않습니다. 아티팩트만 새 컴퓨터로 갑니다」.

---

## 6. 계약 변경 제안 (계약 파일은 고치지 않았다 — Director 승인 PR 로)

### daemon-protocol (v0.9.2 → v0.10.0 제안)

1. **§4.1 `workdir.path` 는 `dir` 도 서버가 짓는다.** 지금 문장은 「서버가 probe `workdir_root` 와 방·에이전트로 조립」인데 실제로는 worktree 만 서버, dir 은 데몬(T-S21 결정 A). 제안: **모든 kind 에서 path 필수·절대**, 규칙은 §6 에 표로(§3 a). 데몬 `Path()`(sessions/…)는 옛 서버 번들(path 없음) 호환용으로만 남는다.
2. **§4.1 `workdir.id` 는 첫 attempt 부터 필수**(서버가 행을 먼저 만든다 — path 를 서버가 지으니 결정 A 의 전제가 사라진다). §6 짝 맞추기 폴백은 옛 데몬·옛 폴더용으로만.
3. **§4.1 `workdir.shared_path?`** — 미션 공용 폴더 절대 경로(미션 밖·테스트 채팅이면 없음). 데몬은 `mkdir -p` 만, 내용은 건드리지 않는다.
4. **§4.1 `task.work_id`** — 이미 있음(v0.9.0). 변화 없음.
5. **§6 보고 행 `work_id?`·`role?`(`agent`|`shared`)** — id 로 찾으므로 필수 아님, S13·GC 진단용.
6. **§6 경로 규칙 표**(§1 트리)와 **데몬 방어**: 데몬은 `rooms/` 아래 경로의 `..`·심링크 탈출을 `UnderRoot` 로 거부(이미 있음) — 문장만.
7. **§4.3 `gc`**: 페이로드 모양 그대로(`workdirs:[{id,path}]`). 문장 추가 — 「데몬은 삭제 뒤 비게 된 `rooms/<room>/<mission>/`·`rooms/<room>/` 상위 폴더를 지운다(비어 있을 때만)」.

### harness (§10 v0.9.x → 다음)

8. **턴 프롬프트 `<folders>` 블록**(§3 b 모양) — 서버가 쓰고 데몬은 건드리지 않는다(절대 경로를 서버가 알기 때문에 자리표시자 불필요). hermes `COLAB_BRIEF.md` 포인터 줄 뒤, `<roster_status>` 옆.
9. **브리프 [2] 고정 한 줄**(§3 b). [1]~[5] 바이트 동일 규칙 유지.
10. `COLAB_BRIEF.md` 는 에이전트 폴더에 쓴다(지금과 같다 — cwd). `_shared` 에는 쓰지 않는다.

### openapi (Workdir)

11. `Workdir` 에 `work_id: uuid|null`, `work: WorkRef|null`(현재 이름), `role: agent|shared`. `listRuntimeWorkdirs` 에 `?work_id=` 필터 — S13 트리 그룹핑과 미션 닫기 확인의 「N개(용량)」 에 필요.
12. `Workdir.agent_id` 설명 문구: 「worktree — 에이전트당 1개」 → 「worktree: 방×에이전트 · dir: 미션×에이전트 · shared 행이면 null」. `lane_id` 설명: 「dir 은 더 이상 lane 당이 아니다 — 첫 lane id(진단용)」.

---

## 7. 위험

| 위험 | 대응 |
|---|---|
| 같은 에이전트 병렬 lane 이 한 폴더에서 같은 파일을 덮어씀(D3 A) | 턴 프롬프트 갈래 표지 규약 · 실측 뒤 D3 C 로 갈 여지(행 모양은 그대로, 경로만 하위) |
| 형제 폴더 쓰기 규약 위반 | D5 — 규약 + 2단계 후보 claude_code deny. 위반은 `git`/mtime 으로 사후 관찰만 가능 |
| 한글 방 이름 슬러그가 `x` | D1 하위 결정(경로 한글 보존) |
| 옛·새 배치 공존으로 데몬 `List` 가 세 트리 | 구조가 고정 깊이라 단순 — `.colab-workdir.json` 표식이 id 를 준다 |
| `none` 경로를 서버가 지으면서 `workdir_root` 미보고 런타임이 dispatch 불가 | worktree 와 같은 거부(`errNoWorkdirRoot`)·피드 문장 — probe 는 v0.7.3 부터 root 를 보낸다 |

---

## 8. 판정 (Director 2026-09-26) — 구현이 따른 결론

| # | 판정 | 이 문서의 권고와 다른 점 · 구현 |
|---|---|---|
| **D1** | **A** — 조각은 `<slug>-<id8>`, 만들 때 고정. **하위 결정: 경로 슬러그는 한글 보존(`PathSlug`), git 브랜치는 ASCII `Slug`** | §3 a 가 열어 둔 하위 결정이 닫혔다. `workdirs.PathSlug`(NFC → 소문자 → `\p{L}`·`\p{N}`·`_` 보존 → 나머지 `-` 하나 → 40룬) · 충돌 시 그 경로의 id 조각을 12자리로 |
| **D2** | **A** — 같은 미션 형제 폴더는 읽기 허용, 쓰기는 자기 폴더 + `_shared` | 권고대로. 알림은 턴 프롬프트 `<folders>` |
| **D3** | **A** — 같은 에이전트의 여러 lane 은 한 폴더를 공유하고 병렬을 유지 | 권고대로. 한 `workdir` 행을 여러 lane 이 가리키고, `<folders>` 가 lane 표지(`<lane_id[:8]>`)를 지시 |
| **D4** | **A** — 미션 공용 `_shared` 를 둔다 | 권고대로. `role=shared` 행(에이전트·lane 없음), 데몬은 `mkdir -p` 만 |
| **D5** | **A** — 쓰기 제한은 **규약만**(`permissions.deny` 강제 없음) | 권고대로. 브리프 [2] 고정 한 줄 + `<folders>` |
| **D6** | **A** — 옛 `sessions/…`·`worktrees/…` 폴더는 옮기지 않는다 | 권고대로. `lane.workdir_id` 가 가리키는 행이 있으면 저장 경로를 그대로 싣는다 |
| **D7** | **C** — 체크아웃은 방×에이전트 그대로 + 미션 `_shared` 만 추가 | 권고대로. 추가 판정: **새 체크아웃 브랜치의 방 조각에 `room_id` 앞 8자리**(`colab/<Slug(방)>-<room_id8>/<Slug(에이전트)>`) — 한글 방 이름이 `Slug` 에서 `x` 가 되어 같은 저장소의 두 방이 겹치던 성질을 막는다 |
| **D8** | **B** — **이 문서의 권고(A 즉시 삭제)와 다르다.** `none` 미션 폴더(에이전트 행 · `_shared`)는 미션이 닫힌 뒤 `last_used_at + workdir_retention_days` 로 GC | §3 d 표와 §2 D8 행의 권고가 뒤집혔다. **미션 닫힘은 삭제 트리거가 아니다** — 닫기는 `gc` 를 싣지 않고(`sessions.gcWorkdirs` 는 `work_id IS NULL` 인 옛 배치 행만), 스윕이 행의 `work_id` 로 판정한다(`workdirs.missionFolderDisposable`). 닫기 확인 문장도 「N개(용량)가 정리됩니다」 → 「N개(〈용량〉)는 〈retention〉일 뒤 정리됩니다」 |

**FINDING-1 은 이 라운드에서 고쳤다** — 새 체크아웃의 슬러그 재료가 `COALESCE(work.title, room.name)` 에서 **방 이름**으로 바뀌었다(이미 만들어진 체크아웃은 행의 저장 경로·브랜치를 그대로 쓴다).

**구현 위치**(2단계): 마이그레이션 `server/migrations/0038_workdir_mission_folders.sql`(`workdir.work_id`·`role`·CHECK 완화·인덱스) · 경로 규칙 `server/internal/workdirs/layout.go` · 번들과 `<folders>` `server/internal/queue/folders.go` · GC `server/internal/workdirs/sweep.go`·`server/internal/sessions/complete.go` · 데몬 `daemon/internal/workdir/{workdir,worktree,marker}.go`·`daemon/internal/brief/brief.go` · 화면 `web/lib/workdir-tree.ts`·S13·`components/CloseWorkDialog.tsx`·`components/RebindDialog.tsx`.
