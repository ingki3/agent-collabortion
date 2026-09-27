#!/usr/bin/env bash
# T-MEDIA 스크린샷 — 미디어 미리보기 · 작성창 파일 붙이기(SCREEN v0.19.12 §4.6 · COMPONENTS §9.12 ·
# PRD FR-4.3.1 · FR-3.7 · Pencil 프레임 eSZM9 「S7-A 파일 붙이기·미디어 미리보기 (v0.19.12)」).
# `next build && next start`(목) 로 찍는다 — 개발 오버레이 배지가 없게. 다른 shots 스크립트와 같은 구조.
#
#   media-01-timeline-light.png   S7 타임라인 — 에이전트가 낸 이미지(썸네일·체커 배경) · 영상(재생기, 첫 프레임) · 소리(한 줄 재생기),
#                                 사람 말풍선 아래 첨부 카드 둘(이미지·mp3). 카드 머리줄(이름·vN·크기·내려받기)은 종류와 무관.
#   media-02-lightbox-light.png   썸네일을 눌러 라이트박스 — 90vw×90vh · ✕ · ← → (같은 묶음 이미지 둘) · 아래 이름·버전·내려받기.
#   media-03-composer-chips-light.png 작성창 첨부 칩 4상태 — 이미지 썸네일 칩(올라감) · 소리 칩 · 올리는 중(진행 막대) · 오류(다시 시도).
#                                 「보내기」는 비활성이고 「파일을 올리는 중입니다」가 붙는다.
#   media-04-timeline-dark.png    01 과 같은 상태, 다크.
#   media-05-lightbox-dark.png    02 와 같은 상태, 다크(라이트박스는 테마와 무관하게 어둡다 — --media-* 토큰).
#   media-06-narrow-700-light.png 좁은 화면 700px — media-preview.css 의 @media (max-width: 720px) 아래.
#                                 썸네일·재생기가 폭 100% 로 떨어진다.
#   media-07-file-fallback-light.png 미리보기 대상 아닌 것(HTML·zip)은 파일 카드 + 「열기」 — image/png 라고 속인 HTML 이
#                                 이미지로 그려지지 않는다는 화면 쪽 증거.
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3198 &
#   BASE_URL=http://localhost:3198 SHOT_DIR=__screenshots__/media bash e2e/media-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3198}"
SHOT_DIR="${SHOT_DIR:-__screenshots__/media}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-media-shots-$$}"

ab() { agent-browser "$@"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
diag() { ab eval 'JSON.stringify({ url: location.pathname, kinds: [...document.querySelectorAll("[data-testid=media-preview]")].map((e) => e.dataset.kind), msgs: document.querySelectorAll("[data-testid=message-card]").length, artref: document.querySelectorAll("[data-testid=artifact-ref]").length, txt: document.body.innerText.slice(0, 600) })' --json | python3 -c 'import sys,json;d=json.load(sys.stdin);print("  진단:",d["data"]["result"] if d["success"] else d["error"])'; }
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; diag; exit 1; }; echo "  ✓ $2"; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return 'ok'})()" >/dev/null; }
open_room() {
  ab open "$BASE_URL/rooms/$RID" >/dev/null
  ab wait '[data-testid="timeline"]' --timeout 20000 >/dev/null
  sleep 2
}
to_bottom() { apic '(function(){var e=document.querySelector("[data-testid=timeline-end]");if(e)e.scrollIntoView({block:"end"});return "ok"})()' >/dev/null; sleep 1; }
trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 초기화 · 로그인 · 방(Lead·Researcher) · 미디어 시드"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 900 >/dev/null
login demo@colab.dev
set_theme light
# 파일은 e2e/assets/media/ 의 **실물**이다(ffmpeg 로 만든 PNG·JPEG·MP4·MP3 + SVG + image/png 로 이름만 붙인 HTML).
# 브라우저 안에서 바이트를 조립하면 「진짜 미디어가 정말 재생되는가」를 못 보여 준다 — 판정도 재생도 실물로 한다.
# notes.png 는 HTML 이다. 목·서버 모두 첫 바이트로 text/html 로 판정하므로 이미지로 그려지면 안 된다.
ASSETS=e2e/assets/media
b64of() { python3 -c 'import base64,sys;print(base64.b64encode(open(sys.argv[1],"rb").read()).decode())' "$1"; }
RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const r = await post(`/workspaces/${me.workspaces[0].id}/rooms`, { name: "게임 제작" });
  await post(`/__mock/rooms/${r.id}/seed`, { agents: ["Lead", "Researcher"], messages: [
    { content: "@Lead AE86 뒷모습 헤드라이트 시안을 만들어 주세요. 레퍼런스는 아래에 붙였습니다." },
  ] });
  return r.id;
})()')
echo "  room=$RID"
# 목 저장소는 브라우저 안에 있다 — 파일을 base64 로 넘겨 브라우저에서 게시한다.
seed_js() { # seed_js AGENT CONTENT FILE...
  local agent="$1" content="$2"; shift 2
  local files="[" first=1
  for f in "$@"; do
    [ $first = 1 ] || files="$files,"; first=0
    files="$files{name:\"$(basename "$f")\",type:\"file\",b64:\"$(b64of "$f")\"}"
  done
  files="$files]"
  apic "(async () => { const r = await fetch('/api/v1/__mock/rooms/$RID/seed-media', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ agent: '$agent', content: '$content', files: $files }) }); return r.status; })()"
}
seed_js Lead "헤드라이트 시안 두 장과 플레이 영상을 올렸습니다. 왼쪽이 원안, 오른쪽이 수정안입니다." "$ASSETS/headlight-a.png" "$ASSETS/headlight-b.png" "$ASSETS/play.mp4"
seed_js Researcher "BGM 초안입니다." "$ASSETS/bgm-draft.mp3"
seed_js Lead "참고 문서와 로고도 올렸습니다." "$ASSETS/notes.png" "$ASSETS/logo.svg"
# 사람이 붙인 파일(FR-3.7) — 작성창이 하는 일 그대로: type attachment 로 업로드 → attachment_ids 로 게시.
apic "(async () => {
  const bytes = (b64) => Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  const up = async (name, b64) => {
    const fd = new FormData();
    fd.append('name', name); fd.append('type', 'attachment');
    // 올리는 쪽은 octet-stream 이라 한다 — 종류는 서버가 첫 바이트로 정한다(FR-4.3.1).
    fd.append('file', new File([bytes(b64)], name, { type: 'application/octet-stream' }), name);
    const r = await fetch('/api/v1/rooms/$RID/artifacts', { method: 'POST', body: fd }).then((x) => x.json());
    return r.artifact.id;
  };
  const a1 = await up('ae86.jpg', '$(b64of "$ASSETS/ae86.jpg")');
  const a2 = await up('bgm-ref.mp3', '$(b64of "$ASSETS/bgm-ref.mp3")');
  // image/png 라고 이름 붙인 HTML — 첨부 카드에서도 이미지로 그려지지 않고 파일 카드 + 「열기」가 된다.
  const a3 = await up('notes.png', '$(b64of "$ASSETS/notes.png")');
  const r = await fetch('/api/v1/rooms/$RID/messages', { method: 'POST', headers: { 'content-type': 'application/json', 'idempotency-key': crypto.randomUUID() }, body: JSON.stringify({ content: '이 사진처럼 뒷모습 헤드라이트를 바꿔 주세요. BGM 은 이 느낌으로요.', attachment_ids: [a1, a2, a3] }) });
  return r.status;
})()"

step "01 — 타임라인(라이트): 썸네일 · 영상 · 소리 · 사람 첨부 카드"
# 한 장에 네 종류를 다 담으려면 세로가 길어야 한다 — 에이전트 턴(이미지 둘·영상·소리)과 사람 첨부가 같이 보인다.
ab set viewport 1280 1500 >/dev/null
open_room
to_bottom
assert_js 'document.querySelectorAll("[data-testid=media-preview][data-kind=image]").length >= 2' "이미지 미리보기 둘 이상"
assert_js 'document.querySelectorAll("[data-testid=media-video]").length >= 1' "영상 재생기"
assert_js 'document.querySelectorAll("[data-testid=media-audio]").length >= 2' "소리 재생기(에이전트 BGM · 사람 첨부)"
assert_js 'document.querySelectorAll("[data-testid=message-attachments]").length >= 1' "사람 말풍선 아래 첨부 카드"
assert_js '[...document.querySelectorAll("[data-testid=media-video]")].every((v) => v.hasAttribute("controls") && v.getAttribute("preload") === "metadata" && !v.hasAttribute("autoplay"))' "영상 controls·preload=metadata·자동 재생 없음"
assert_js '[...document.querySelectorAll("[data-testid=media-image]")].every((i) => i.getAttribute("src").includes("inline=true"))' "미리보기는 ?inline=true"
shot media-01-timeline-light

step "02 — 라이트박스(라이트)"
ab set viewport 1280 900 >/dev/null
open_room
to_bottom
apic '(function(){document.querySelectorAll("[data-testid=media-image-open]")[0].click();return "ok"})()' >/dev/null
sleep 1
assert_js 'document.querySelector("[data-testid=lightbox]") !== null' "라이트박스 열림"
assert_js 'document.querySelector("[data-testid=lightbox-next]") !== null && document.querySelector("[data-testid=lightbox-prev]") !== null' "← → (같은 묶음 이미지 둘)"
assert_js 'document.activeElement === document.querySelector("[data-testid=lightbox-close]")' "✕ 에 초점"
assert_js 'getComputedStyle(document.querySelector("[data-testid=lightbox]")).backgroundColor === "rgba(0, 0, 0, 0.8)"' "덮개 rgba(0,0,0,.8) (COMPONENTS §9.12)"
sleep 1
shot media-02-lightbox-light
apic '(function(){document.querySelector("[data-testid=lightbox-close]").click();return "ok"})()' >/dev/null
sleep 1

step "03 — 작성창 첨부 칩 4상태(라이트)"
# 칩 상태 넷을 한 화면에 두려면 업로드가 끝나지 않아야 한다 — 목 업로드를 잡아 두고(지연·실패) 칩을 만든다.
apic '
(() => {
  const orig = window.XMLHttpRequest;
  let n = 0;
  // 3번째 파일은 올리는 중(진행 40%)에 멈추고, 4번째는 실패한다. 1·2 는 바로 끝난다.
  window.XMLHttpRequest = function () {
    const xhr = new orig();
    const which = n++;
    const send = xhr.send.bind(xhr);
    xhr.send = (body) => {
      if (which === 2) { setTimeout(() => xhr.upload.onprogress && xhr.upload.onprogress({ lengthComputable: true, loaded: 40, total: 100 }), 50); return; }
      if (which === 3) { setTimeout(() => { Object.defineProperty(xhr, "status", { value: 502 }); Object.defineProperty(xhr, "responseText", { value: JSON.stringify({ detail: "올리다 끊겼습니다 — 잠시 뒤 다시 시도해 주세요" }) }); xhr.onload && xhr.onload(); }, 80); return; }
      send(body);
    };
    return xhr;
  };
  return "hooked";
})()' >/dev/null
apic "
(() => {
  const bytes = (b64) => Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  const dt = new DataTransfer();
  // 실물 바이트 — 칩 썸네일은 고른 파일을 그대로 읽어 그린다(올라가기 전에도 보인다).
  dt.items.add(new File([bytes('$(b64of "$ASSETS/ae86.jpg")')], 'ae86.jpg', { type: 'image/jpeg' }));
  dt.items.add(new File([bytes('$(b64of "$ASSETS/bgm-ref.mp3")')], 'bgm-ref.mp3', { type: 'audio/mpeg' }));
  dt.items.add(new File([bytes('$(b64of "$ASSETS/headlight-b.png")')], 'rear.png', { type: 'image/png' }));
  dt.items.add(new File([bytes('$(b64of "$ASSETS/play.mp4")')], 'clip.mp4', { type: 'video/mp4' }));
  const input = document.querySelector('[data-testid=composer-file-input]');
  input.files = dt.files;
  input.dispatchEvent(new Event('change', { bubbles: true }));
  const ta = document.querySelector('[data-testid=composer-input]');
  ta.value = '@Lead 이 사진처럼 뒷모습 헤드라이트를 바꿔 주세요';
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  return 'picked';
})()" >/dev/null
sleep 2
assert_js 'document.querySelectorAll("[data-testid=attach-chip]").length === 4' "첨부 칩 넷"
assert_js 'document.querySelector("[data-testid=attach-progress]") !== null' "올리는 중 진행 막대"
assert_js 'document.querySelector("[data-testid=attach-retry]") !== null' "오류 칩의 다시 시도"
assert_js 'document.querySelector("[data-testid=composer-send]").disabled === true' "다 올라가기 전 보내기 비활성"
assert_js 'document.querySelector("[data-testid=attach-pending]").innerText.includes("올리는 중")' "「파일을 올리는 중입니다」"
apic '(function(){var c=document.querySelector("[data-testid=composer]");c.scrollIntoView({block:"end"});return "ok"})()' >/dev/null
sleep 1
shot media-03-composer-chips-light

step "04·05 — 다크"
set_theme dark
ab set viewport 1280 1500 >/dev/null
open_room
to_bottom
assert_js 'document.querySelectorAll("[data-testid=media-preview][data-kind=image]").length >= 2' "다크에도 썸네일 둘"
shot media-04-timeline-dark
ab set viewport 1280 900 >/dev/null
open_room
to_bottom
apic '(function(){document.querySelectorAll("[data-testid=media-image-open]")[0].click();return "ok"})()' >/dev/null
sleep 1
assert_js 'getComputedStyle(document.querySelector("[data-testid=lightbox]")).backgroundColor === "rgba(0, 0, 0, 0.8)"' "라이트박스 덮개는 테마와 무관하게 어둡다"
sleep 1
shot media-05-lightbox-dark
apic '(function(){document.querySelector("[data-testid=lightbox-close]").click();return "ok"})()' >/dev/null

step "06 — 좁은 화면 700px(라이트)"
set_theme light
ab set viewport 700 1000 >/dev/null
open_room
to_bottom
assert_js '(function(){var t=document.querySelector("[data-testid=media-image]");var card=t.closest("[data-testid=media-preview]");return t.getBoundingClientRect().width <= card.getBoundingClientRect().width + 1})()' "≤720px — 썸네일이 카드 폭 안"
shot media-06-narrow-700-light

step "07 — 미리보기 아닌 것(HTML)은 이미지로 그려지지 않는다"
ab set viewport 1280 900 >/dev/null
open_room
to_bottom
# 첨부 카드는 MediaPreview 를 지나므로 notes.png 는 kind=file 폴백(「열기」)이 된다.
assert_js '[...document.querySelectorAll("[data-testid=media-preview]")].filter((c) => c.dataset.kind === "file").length >= 1' "첨부 카드에 파일 폴백"
assert_js '[...document.querySelectorAll("[data-testid=media-preview]")].every((c) => c.dataset.kind !== "image" || !c.innerText.includes("notes.png"))' "image/png 라고 속인 HTML 이 이미지로 그려지지 않는다"
assert_js '[...document.querySelectorAll("[data-testid=media-preview][data-kind=file]")].every((c) => c.querySelector("img, video, audio") === null)' "파일 카드에는 재생기·이미지가 없다"
assert_js '[...document.querySelectorAll("[data-testid=media-preview][data-kind=file]")].some((c) => c.querySelector("[data-testid=media-open]").innerText === "열기")' "「열기」"
apic '(function(){var f=document.querySelector("[data-testid=media-preview][data-kind=file]");if(f)f.scrollIntoView({block:"center"});return "ok"})()' >/dev/null
sleep 1
shot media-07-file-fallback-light

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "== media-shots: 7장 ($SHOT_DIR) =="
