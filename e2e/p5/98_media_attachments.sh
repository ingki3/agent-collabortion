#!/usr/bin/env bash
# e2e/p5/98_media_attachments.sh — T-MEDIA: 미디어 미리보기 · 파일 붙이기(PRD v0.19.10 FR-4.3.1 · FR-3.7 ·
# openapi v0.3.7 · colab-cli v0.9.6 · harness v0.9.12) — 실서버 + 실데몬 + 실 CLI, 모델만 페이크.
#
# 비용 한 줄(I-3): 페이크 턴 1 · $0 · ≈ 25s
#
# 사람 → 에이전트 (FR-3.7)
#   M1  사람이 작성창처럼 이미지를 올린다(submitArtifact type attachment, multipart) → content_type 은 **서버가 판정**
#       (올리는 쪽이 application/octet-stream 이라 해도 image/png)
#   M2  그 id 를 attachment_ids 로 실어 에이전트를 멘션 → Message.attachments 에 그 버전 그대로
#   M3  깨어난 턴의 <trigger> 에 `Attachments:` 줄(이름·type·content_type·크기·id)과 표면별 받는 법 한 줄
#   M4  에이전트가 `colab artifact get <id> --out` 으로 작업 폴더에 받는다 — 바이트가 올린 것과 같다
#   M5  브리프 [2] 에 미디어 한 줄(harness v0.9.12), 셸 표면이면 `colab artifact submit` 로
# 에이전트 → 사람 (FR-4.3.1)
#   M6  에이전트가 mp3 를 제출 → content_type audio/mpeg (판정), 그 아티팩트를 `--attach` 로 말에 붙인다
#   M7  미리보기: `?inline=true` → Content-Disposition inline · nosniff · CSP sandbox
#   M8  Range: 단일 범위 206 + Content-Range·바이트 일치 / 여러 범위 416
#   M9  안전: HTML 을 image/png 라고 올려도 image 로 판정되지 않고 inline 도 안 된다 · SVG 는 inline 이되 sandbox
#   M10 경계: 다른 방 아티팩트를 attachment_ids 에 → 422 attachment_not_in_room · TaskToken 은 자기 방만
#   M11 type attachment 는 종료 조건 artifact_submitted 를 채우지 않는다(사람이 올렸어도, 에이전트가 올렸어도)
# 산출물: out/98-checks.tsv · out/98-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-98.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-98.json"; WORK="$P5_TMP_ROOT/98/work"; DLOG="$OUT/daemon-98.log"
TAP="$OUT/tap-98.jsonl"; TAP_PORT="${TAP_PORT_98:-8150}"
MEDIA="$OUT/98-media"; mkdir -p "$MEDIA"
g5_chk_init "$OUT/98-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-98.pid"; return 0; }
trap cleanup EXIT

step "0. 파일 넷 — png(사람 첨부) · mp3(에이전트 제출) · html(image/png 사칭) · svg"
python3 - "$MEDIA" <<'PY'
import struct, sys, zlib, pathlib
d = pathlib.Path(sys.argv[1])
def chunk(t, b):
    return struct.pack(">I", len(b)) + t + b + struct.pack(">I", zlib.crc32(t + b) & 0xFFFFFFFF)
w = h = 64
rows = b"".join(b"\x00" + b"".join(bytes([(x * 4) % 256, (y * 4) % 256, 120]) for x in range(w)) for y in range(h))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")
(d / "ae86.png").write_bytes(png)
# ID3 헤더 + 무음 프레임 채움 — 서버는 첫 바이트로 audio/mpeg 로 판정한다(재생 가능성은 이 테스트의 대상이 아니다).
(d / "bgm.mp3").write_bytes(b"ID3\x03\x00\x00\x00\x00\x00\x00" + bytes(range(256)) * 80)
(d / "evil.png").write_bytes(b"<!DOCTYPE html><html><body><script>alert(document.cookie)</script></body></html>")
(d / "logo.svg").write_bytes(b'<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><script>alert(1)</script><rect width="8" height="8" fill="red"/></svg>')
PY
ls -l "$MEDIA" > "$OUT/98-files.txt"

step "1. claim 탭 · 계정 · 워크스페이스 · 페어링 (RUNTIME=$RUNTIME)"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
signup "media+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Media $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 2
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-98.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

step "2. Designer(hermes = 셸 표면) · 방 — 대본은 첨부를 받아 mp3 를 내고 --attach 로 붙인다"
# 셸 표면(hermes)을 고른 이유: <trigger> 의 받는 법 줄과 브리프 [2] 의 미디어 줄이 `colab …` 셸 명령으로 와야 하고
# (harness v0.9.12), 대본이 그 CLI 를 실제로 부른다 — 표면 문장과 실행이 한 판에서 맞는지 본다.
DESIGNER="$(create_agent_fake "$WS" Designer custom hermes "$LEAD_MODEL" \
  "레퍼런스 이미지를 받아 시안을 만든다. 첨부로 받은 파일은 artifact get 으로 받아 확인하고, 만든 것은 artifact submit 으로 낸다." \
  '시안을 만든다' "$(jq -nc --arg fix "$FIX" --arg m "$MEDIA" '{turns:[{steps:[{exec:("bash "+$fix+"/agent.sh Designer "+$m)}]}]}')")"
# 부분 메시지는 받는 쪽이 둘 이상이어야 한다(openapi minItems 2) — Designer 가 Dir 과 R 에게 서로 다른 파일을 준다.
# R 은 깨어나도 할 일이 없는 대본(아무것도 안 한다)이다: 이 판이 보는 것은 R 의 턴이 아니라 부분마다의 첨부다.
REVIEWER="$(create_agent_fake "$WS" R reviewer hermes "$LEAD_MODEL" \
  "받은 시안을 본다." '검토한다' "$(jq -nc '{turns:[{steps:[{text:"확인했습니다."}]}]}')")"
SESSION="$(create_session_p2 "$WS" "게임 제작" "AE86 뒷모습 헤드라이트 시안을 만든다" "$DESIGNER" "$RUNTIME_ID" "$DESIGNER" "$DESIGNER" "$REVIEWER")"
OTHER="$(create_session_p2 "$WS" "다른 방" "다른 일" "$DESIGNER" "$RUNTIME_ID" "$DESIGNER" "$DESIGNER")"
echo "$WS $SESSION $OTHER $DESIGNER $REVIEWER $RUNTIME_ID" > "$OUT/98-ids.txt"
wait_quiet "$SESSION" "$T_TURN" || true

step "3. M1 — 사람이 이미지를 올린다(작성창이 하는 일: submitArtifact type attachment)"
upload() { # upload ROOM FILE NAME CLAIMED_CT [TYPE] → artifact json
  local room="$1" file="$2" name="$3" ct="$4" typ="${5:-attachment}"
  curl -sS -b "$COOKIE" -c "$COOKIE" -X POST "$API/rooms/$room/artifacts" \
    -F "name=$name" -F "type=$typ" -F "file=@$file;type=$ct"
}
A_PNG_J="$(upload "$SESSION" "$MEDIA/ae86.png" ae86.png application/octet-stream)"
A_PNG="$(jq -r '.artifact.id' <<<"$A_PNG_J")"
chk M1a "사람이 올린 png 의 content_type 은 서버 판정(올리는 쪽은 octet-stream 이라 했다)" image/png "$(jq -r '.artifact.content_type' <<<"$A_PNG_J")"
chk M1b "종류 attachment · 버전 1 · 크기 = 파일" "attachment|1|$(wc -c < "$MEDIA/ae86.png" | tr -d ' ')" \
  "$(jq -r '[.artifact.type,(.artifact.version|tostring),(.artifact.size_bytes|tostring)]|join("|")' <<<"$A_PNG_J")"

step "4. M9 — 사칭 HTML · SVG 판정"
A_HTML="$(jq -r '.artifact.id' <<<"$(upload "$SESSION" "$MEDIA/evil.png" evil.png image/png)")"
A_SVG="$(jq -r '.artifact.id' <<<"$(upload "$SESSION" "$MEDIA/logo.svg" logo.svg image/svg+xml)")"
chk M9a "image/png 이라고 속인 HTML 은 image 로 판정되지 않는다" text/html \
  "$(api_ok GET "/artifacts/$A_HTML" | jq -r '.content_type | split(";")[0]')"
chk M9b "SVG 는 image/svg+xml" image/svg+xml "$(api_ok GET "/artifacts/$A_SVG" | jq -r '.content_type')"

step "5. M2·M10 — attachment_ids 로 게시(같은 방만)"
A_OTHER="$(jq -r '.artifact.id' <<<"$(upload "$OTHER" "$MEDIA/ae86.png" ae86.png image/png)")"
POST_BAD="$(api POST "/rooms/$SESSION/messages" \
  "$(jq -nc --arg m "[@Designer](mention://agent/$DESIGNER) 다른 방 파일" --arg a "$A_OTHER" '{content:$m,attachment_ids:[$a]}')" \
  -H "Idempotency-Key: $(uuid)")"
chk M10a "다른 방 아티팩트 첨부 → 422 attachment_not_in_room" "422|attachment_not_in_room" \
  "$(api_code <<<"$POST_BAD")|$(api_body <<<"$POST_BAD" | jq -r '.errors[0].code')"
POST_J="$(api_ok POST "/rooms/$SESSION/messages" \
  "$(jq -nc --arg m "[@Designer](mention://agent/$DESIGNER) 이 사진처럼 뒷모습 헤드라이트를 바꿔 주세요" --arg a "$A_PNG" \
     '{content:$m,attachment_ids:[$a,$a]}')" -H "Idempotency-Key: $(uuid)")"
M_HUMAN="$(jq -r '.message.id' <<<"$POST_J")"
chk M2a "Message.attachments = 그 아티팩트 한 건(중복은 한 번), 버전 그대로" "$A_PNG|1|image/png" \
  "$(jq -r '[.message.attachments[]|[.artifact_id,(.version|tostring),.content_type]|join("|")]|join(" ")' <<<"$POST_J")"
T_D="$(jq -r '.triggers[0].task_id' <<<"$POST_J")"
chk M2b "그 메시지가 에이전트를 깨웠다" yes "$( [ -n "$T_D" ] && [ "$T_D" != null ] && echo yes || echo no )"

step "6. M3·M4·M5 — 턴 번들(탭)과 대본이 받은 파일"
WAIT_S=$T_TURN wait_task "$T_D" completed failed cancelled >/dev/null || true
python3 - "$TAP" "$T_D" "$A_PNG" "$(wc -c < "$MEDIA/ae86.png" | tr -d ' ')" > "$OUT/98-bundle.txt" <<'PY'
import json, sys
tap, tid, aid, size = sys.argv[1:]
want_line = f"- ae86.png (attachment, image/png, {round(int(size)/1024)} KB, id {aid})"
for line in open(tap):
    try: body = json.loads(line)["body"]
    except Exception: continue
    for b in (body.get("tasks") or []):
        if b["task"]["id"] != tid: continue
        p = b["prompt"]; trig = p[p.find("<trigger>"):p.find("</trigger>")]
        brief = b["brief"]["text"]
        # 받는 법은 셸 표면(hermes)이므로 `colab artifact get … --out …`, 브리프 [2] 는 `colab artifact submit`.
        print("yes" if "Attachments:\n" in trig else "no",
              "yes" if want_line in trig else "no:" + repr([l for l in trig.splitlines() if l.startswith("- ae86")]),
              "yes" if "Download one into your folder with `colab artifact get <id> --out <path>`." in trig else "no",
              "yes" if "submit them with `colab artifact submit`" in brief else "no",
              "yes" if "colab_artifact" not in brief + p else "no",
              sep="\t")
        sys.exit(0)
print("missing\tmissing\tmissing\tmissing\tmissing")
PY
IFS=$'\t' read -r B_HEAD B_LINE B_FETCH B_BRIEF B_SHELL <<<"$(cat "$OUT/98-bundle.txt")"
chk M3a "<trigger> 에 Attachments: 줄" yes "$B_HEAD"
chk M3b "첨부 한 줄 = 이름 (type, content_type, 크기, id …)" yes "$B_LINE"
chk M3c "받는 법 한 줄(셸 표면)" yes "$B_FETCH"
chk M5a "브리프 [2] 미디어 한 줄(셸 표면)" yes "$B_BRIEF"
chk M5b "셸 표면 번들에 MCP 툴 이름 0" yes "$B_SHELL"
GOT="$(ls "$OUT/fake-records"/got-*.png 2>/dev/null | head -1)"
chk M4 "대본이 artifact get 으로 받은 바이트 = 올린 파일" "$(shasum -a 256 < "$MEDIA/ae86.png" | cut -d' ' -f1)" \
  "$( [ -n "$GOT" ] && shasum -a 256 < "$GOT" | cut -d' ' -f1 || echo missing )"

step "7. M6 — 에이전트가 낸 mp3 와 --attach 로 붙인 말"
A_MP3="$(psqlq "select id from artifact where session_id='$SESSION' and name='bgm.mp3' order by created_at desc limit 1")"
chk M6a "에이전트가 낸 mp3 의 content_type 은 판정값" audio/mpeg \
  "$(psqlq "select content_type from artifact where id='$A_MP3'")"
# 부분 메시지도 mp3 를 붙이므로(10단계) 「마지막 에이전트 메시지」로 고르면 그때그때 달라진다 —
# --attach 로 붙인 그 말(group_id 가 없는 에이전트 메시지)을 콕 집는다.
chk M6b "에이전트 메시지에 그 아티팩트가 첨부로 붙었다(--attach)" "$A_MP3" \
  "$(psqlq "select ma.artifact_id from message_attachment ma join message m on m.id=ma.message_id
            where m.session_id='$SESSION' and m.author_type='agent' and m.group_id is null
            order by m.created_at desc limit 1")"
chk M6c "listMessages 의 Message.attachments 가 그 첨부를 싣는다" "bgm.mp3|audio/mpeg" \
  "$(api_ok GET "/rooms/$SESSION/messages?limit=200" | jq -r --arg a "$A_MP3" '[.items[].attachments[]? | select(.artifact_id==$a) | [.name,.content_type]|join("|")]|first // "none"')"

step "8. M7·M8 — 미리보기 헤더 · Range"
# 헤더 읽기 — BSD awk 에는 IGNORECASE 가 없다(macOS 실측: 전부 빈 값이었다). 파이썬으로 소문자 키를 뽑는다.
hdr() { curl -sS -D - -o /dev/null -b "$COOKIE" "$API/artifacts/$1/content$2" "${@:3}"; }
# hval HEADERS NAME [NAME...] → 값들을 | 로 이어 출력(없으면 빈 칸). Content-Disposition 은 앞 토막(inline·attachment)만.
hval() { python3 - "$@" <<'PY'
import sys
raw, names = sys.argv[1], sys.argv[2:]
got = {}
for line in raw.splitlines():
    if ":" in line:
        k, v = line.split(":", 1)
        got.setdefault(k.strip().lower(), v.strip())
out = []
for n in names:
    v = got.get(n.lower(), "")
    if n.lower() == "content-disposition":
        v = v.split(";")[0].strip()
    out.append(v)
print("|".join(out))
PY
}
H_INLINE="$(hdr "$A_MP3" "?inline=true")"
chk M7a "?inline=true → Content-Disposition inline · 판정 Content-Type" "inline|audio/mpeg" \
  "$(hval "$H_INLINE" Content-Disposition Content-Type)"
chk M7b "nosniff · CSP sandbox · Accept-Ranges" "nosniff|sandbox|bytes" \
  "$(hval "$H_INLINE" X-Content-Type-Options Content-Security-Policy Accept-Ranges)"
chk M7c "미리보기 목록 밖(HTML)은 inline 이 아니다" attachment \
  "$(hval "$(hdr "$A_HTML" "?inline=true")" Content-Disposition)"
chk M9c "SVG 는 inline 이되 sandbox 로 — 문서로 열려도 스크립트가 돌지 않는다" "inline|sandbox" \
  "$(hval "$(hdr "$A_SVG" "?inline=true")" Content-Disposition Content-Security-Policy)"
MP3_SIZE="$(psqlq "select size_bytes from artifact where id='$A_MP3'")"
R206="$(curl -sS -D "$OUT/98-range.hdr" -o "$OUT/98-range.bin" -w '%{http_code}' -b "$COOKIE" -H 'Range: bytes=100-199' "$API/artifacts/$A_MP3/content?inline=true")"
chk M8a "단일 Range → 206 · Content-Range · 정확히 100바이트" "206|bytes 100-199/$MP3_SIZE|100" \
  "$R206|$(hval "$(cat "$OUT/98-range.hdr")" Content-Range)|$(wc -c < "$OUT/98-range.bin" | tr -d ' ')"
python3 - "$MEDIA/bgm.mp3" "$OUT/98-range.bin" > "$OUT/98-range.txt" <<'PY'
import sys
whole = open(sys.argv[1], "rb").read()
print("same" if open(sys.argv[2], "rb").read() == whole[100:200] else "different")
PY
chk M8b "206 의 바이트 = 원본 [100,200)" same "$(cat "$OUT/98-range.txt")"
chk M8c "여러 범위·잘못된 범위는 416" "416|416|416" \
  "$(for r in 'bytes=0-1,5-6' "bytes=$MP3_SIZE-" 'bytes=9-3'; do
       printf '%s|' "$(curl -sS -o /dev/null -w '%{http_code}' -b "$COOKIE" -H "Range: $r" "$API/artifacts/$A_MP3/content")"
     done | sed 's/|$//')"
chk M8d "Range 없으면 200 + 전체 길이" "200|$MP3_SIZE" \
  "$(curl -sS -o /dev/null -w '%{http_code}|%{size_download}' -b "$COOKIE" "$API/artifacts/$A_MP3/content")"

step "9. M10·M11 — TaskToken 경계 · 종료 조건"
# 대본이 남긴 흔적: 다른 방 아티팩트를 붙이려 한 시도(TaskToken 은 자기 방만)
chk M10b "TaskToken 으로 다른 방 아티팩트 첨부 → 거부(대본 흔적)" refused \
  "$(grep -ho 'attach-other-room=[a-z]*' "$OUT/fake-records"/*.jsonl "$OUT/fake-records/agent-trace.tsv" 2>/dev/null | tail -1 | cut -d= -f2)"
# 종료 조건 진행은 work.completion_met(충족된 원자의 jsonb)에 있다 — 서버가 ApplyWorkEvent 에서 쓴다.
MET="$(psqlq "select coalesce((select count(*) from jsonb_object_keys(completion_met) k where completion_met->>k = 'true')::text,'0') from work where room_id='$SESSION'")"
chk M11a "type attachment 는 artifact_submitted 를 채우지 않는다(사람이 올린 png 3건 뒤에도)" 1 \
  "$(psqlq "select count(*) from artifact where session_id='$SESSION' and type='attachment'" | awk '{print ($1>=1)?1:0}')"
chk M11b "종료 조건 met 은 에이전트의 제출(mp3, type file)만 센다 — artifact_submitted 하나" 1 "${MET:-none}"
chk M11c "met 에 든 것은 artifact_submitted 뿐(attachment 는 아무것도 채우지 않았다)" artifact_submitted \
  "$(psqlq "select string_agg(k, ',' order by k) from work w, jsonb_object_keys(w.completion_met) k where w.room_id='$SESSION' and w.completion_met->>k = 'true'")"

step "10. M12 — 부분 메시지의 첨부는 부분마다(openapi v0.3.7 MessagePartCreate.attachment_ids)"
# 대본이 --parts-file 로 두 부분을 냈다: Dir 에게 mp3, R 에게 그림 사본. 같은 group_id 두 행이 서로 다른 파일 하나씩.
GRP="$(psqlq "select group_id::text from message where session_id='$SESSION' and group_id is not null order by created_at desc limit 1")"
chk M12a "부분 메시지가 게시됐다(같은 group_id 두 행)" 2 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and group_id='${GRP:-00000000-0000-0000-0000-000000000000}'")"
# 부분마다 첨부 하나씩 — 그리고 두 부분의 파일이 서로 다르다(한 목록을 나눠 쓰지 않는다).
chk M12b "부분마다 첨부 하나씩" "1|1" \
  "$(psqlq "select string_agg(c::text, '|' order by gi) from (select m.group_index gi, count(ma.artifact_id) c from message m left join message_attachment ma on ma.message_id = m.id where m.group_id='${GRP:-00000000-0000-0000-0000-000000000000}' group by m.group_index) t")"
chk M12c "두 부분의 파일이 서로 다르다 — 묶음이 한 목록을 나눠 쓰지 않는다" 2 \
  "$(psqlq "select count(distinct ma.artifact_id) from message m join message_attachment ma on ma.message_id = m.id where m.group_id='${GRP:-00000000-0000-0000-0000-000000000000}'")"
chk M12d "부분 게시 exit 0" 0 \
  "$(grep -ho 'parts-attach-exit=[0-9]*' "$OUT/fake-records/agent-trace.tsv" 2>/dev/null | tail -1 | cut -d= -f2)"
# --attach 는 --parts-file 과 못 쓴다(첨부는 부분 안에) — exit 2.
chk M12e "--parts-file 과 --attach 를 같이 주면 exit 2" 2 \
  "$(grep -ho 'parts-plus-attach-exit=[0-9]*' "$OUT/fake-records/agent-trace.tsv" 2>/dev/null | tail -1 | cut -d= -f2)"

printf '\n98_media_attachments: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
