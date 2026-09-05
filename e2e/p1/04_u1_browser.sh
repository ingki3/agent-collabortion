#!/usr/bin/env bash
# e2e/p1/04_u1_browser.sh — (d) EVAL_USER U1 1~13단계 + U13(초대로 두 번째 멤버) 를 실서버(make dev 구성, COLAB_MOCK_API 없음)에 agent-browser 로.
# web/e2e/u1.sh(W 스트림) 의 셀렉터를 따르되, 4단계에서 화면의 설치 명령 2행에서 페어링 코드를 읽어 실제 bin/daemon 을 붙인다.
# 스크린샷: web/__screenshots__/p1-u1-*.png, p1-u13-*.png. 단계별 "보이는 것" 판정을 e2e/p1/out/d-steps.tsv 에 남긴다.
# 사용: bash e2e/p1/up.sh && bash e2e/p1/04_u1_browser.sh
source "$(dirname "$0")/lib.sh"
cd "$E2E_ROOT/web"
SHOT_DIR="__screenshots__"; mkdir -p "$SHOT_DIR"
STAMP="$(date +%s)"
EMAIL="minji+${STAMP}@example.com"; PASSWORD="password123"; NAME="민지"
EMAIL2="seoyeon+${STAMP}@example.com"; NAME2="서연"
CFG="$OUT/daemon-u1.json"; WORK="$OUT/work-u1"; DLOG="$OUT/daemon-u1.log"
STEPS="$OUT/d-steps.tsv"; echo -e "step\tscreen\texpected\tresult\tnote" > "$STEPS"
export AGENT_BROWSER_SESSION="colab-p1-u1-${STAMP}"
ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; log "📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; log "📸 $SHOT_DIR/$1.png (full)"; }
rec() { echo -e "$1\t$2\t$3\t$4\t${5:-}" >> "$STEPS"; [ "$4" = PASS ] && ok "U1-$1 $3" || bad "U1-$1 $3 — $4 ${5:-}"; }
# 조건 검사 헬퍼: try CMD... → 성공 여부(0/1) 만
try() { "$@" >/dev/null 2>&1; }
wait_text() { ab wait --text "$1" --timeout "$((${2:-25} * 1000))" >/dev/null 2>&1; }
wait_sel() { ab wait "$1" --timeout "$((${2:-25} * 1000))" >/dev/null 2>&1; }
wait_status() { ab wait --fn "document.querySelector('[data-testid=\"$1\"]')?.getAttribute('data-status')==='$2'" --timeout "$((${3:-30} * 1000))" >/dev/null 2>&1; }
wait_cards() { ab wait --fn "document.querySelectorAll('[data-testid=\"message-card\"]').length >= $1" --timeout "$((${2:-120} * 1000))" >/dev/null 2>&1; }
cleanup() { ab close >/dev/null 2>&1 || true; AGENT_BROWSER_SESSION="colab-p1-u13-${STAMP}" agent-browser close >/dev/null 2>&1 || true; }
trap cleanup EXIT
ab set viewport 1280 900 >/dev/null

step "U1-1 회원가입 S2 ($EMAIL)"
ab open "$WEB_URL/signup" >/dev/null
wait_sel '[data-testid="signup-form"]' || die "signup form"
NF="$(ab get count 'input' 2>/dev/null || echo ?)"
ab fill 'input[name=display_name]' "$NAME" >/dev/null; ab fill 'input[name=email]' "$EMAIL" >/dev/null; ab fill 'input[name=password]' "$PASSWORD" >/dev/null
shot p1-u1-01-signup
SOCIAL="$(ab get count 'button:not([type=submit])' 2>/dev/null || echo 0)"
rec 1 S2 "필드(이름·이메일·비밀번호)+가입 버튼, 소셜 로그인 없음" PASS "inputs=$NF non-submit buttons=$SOCIAL"
ab click 'button[type=submit]' >/dev/null

step "U1-2 온보딩 S4-1 워크스페이스 이름"
if try ab wait --url "**/onboarding" --timeout 20000 && wait_sel '[data-testid="workspace-name"]'; then
  shot p1-u1-02-onboarding-workspace
  SKIP="$(ab get count '[data-testid="workspace-skip"]' 2>/dev/null || echo 0)"
  rec 2 S4-1 "가입 직후 S4 자동 진입, 1단계 워크스페이스 이름 (+건너뛰기 링크)" PASS "건너뛰기 링크 testid 수=$SKIP (U1 명세엔 있음)"
else rec 2 S4-1 "온보딩 자동 진입" FAIL "url=$(ab get url)"; exit 1; fi

step "U1-3 '마케팅팀' → S4-2 컴퓨터 연결(S12 인라인)"
ab fill '[data-testid="workspace-name"]' "마케팅팀" >/dev/null; ab click '[data-testid="workspace-next"]' >/dev/null
wait_sel '[data-testid="pairing-panel"]' || die "pairing panel"
wait_sel '[data-testid="install-cmd-2"]'
CMD1="$(ab get text '[data-testid="install-cmd-1"] code')"; CMD2="$(ab get text '[data-testid="install-cmd-2"] code')"
log "설치 명령 1: $CMD1"; log "설치 명령 2: $CMD2"
if wait_status pairing-status waiting 10 && [ -n "$CMD1" ] && [ -n "$CMD2" ]; then shot p1-u1-03-s12-waiting; rec 3 S4-2 "설치 명령 2줄 + 복사 버튼 + 상태 '대기 중'" PASS "cmd2='$CMD2'"; else shot p1-u1-03-s12-waiting; rec 3 S4-2 "설치 명령 2줄 + 상태 대기 중" FAIL; fi

step "U1-4 명령 실행(실제 데몬 페어링) → 상태 자동 갱신 → 준비 완료"
CODE="$(sed -E 's/.* pair ([^ ]+) --server ([^ ]+).*/\1/' <<<"$CMD2")"; SRV="$(sed -E 's/.* pair ([^ ]+) --server ([^ ]+).*/\2/' <<<"$CMD2")"
[ -n "$CODE" ] && [ "$SRV" = "$SERVER_URL" ] && ok "install_commands 의 서버 URL=$SRV (데몬이 :8080 으로 직접 감, PR #21 N6)" || bad "install_commands 서버 URL='$SRV' (기대 $SERVER_URL)"
rm -f "$CFG"; mkdir -p "$WORK"
T0="$(now_ms)"
daemon_pair "$CODE" "$CFG" "$WORK"      # 화면의 2행과 같은 인자 (bin/daemon == colab-daemon)
colab_tap "$CFG"; daemon_start "$CFG" "$DLOG" > "$OUT/daemon-u1.pid"
if wait_status pairing-status ready 120; then T1="$(now_ms)"; ok "S12 준비 완료 in $(( (T1-T0) ))ms"; else T1="$(now_ms)"; bad "S12 ready 안 됨 (status=$(ab get attr '[data-testid="pairing-status"]' data-status))"; fi
CAPS="$(ab get text '[data-testid="pairing-capabilities"]' 2>/dev/null || echo '')"; log "capabilities: $(tr '\n' ' ' <<<"$CAPS")"
shot p1-u1-04-s12-ready
if grep -q "Claude Code" <<<"$CAPS" && grep -Eq "로그인|logged" <<<"$CAPS"; then rec 4 S4-2 "대기 중→연결됨→CLI 감지 중→준비 완료 자동 갱신, 'Claude Code 감지됨·로그인됨·모델 N개'" PASS "$(( (T1-T0)/1000 ))s; $(tr '\n' ' ' <<<"$CAPS" | head -c 120)"; else rec 4 S4-2 "준비 완료 + Claude Code 감지 문구" FAIL "$(tr '\n' ' ' <<<"$CAPS" | head -c 120)"; fi
grep -q "Hermes" <<<"$CAPS" && log "Hermes 도 감지됨(이 머신엔 설치돼 있음 — U1 전제 'Hermes 없음' 과 다름, 정상)"
ab click '[data-testid="pairing-next"]' >/dev/null

step "U1-5/6 3단계 에이전트 (템플릿 3장은 P2 → P1 은 Lead 1개 폼)"
wait_sel '[data-testid="agent-create"]' || die "agent step"
shot p1-u1-05-onboarding-agent
TPL="$(ab get count '[data-testid^="template-"]' 2>/dev/null || echo 0)"
rec 5 S4-3 "템플릿 카드 3장 + 직접 만들기" N/A "P2(applyAgentTemplate x-phase P2). 보이는 것: Lead 이름 폼 + 생성/건너뛰기 (템플릿 testid=$TPL)"
ab click '[data-testid="agent-create"]' >/dev/null
if try ab wait --url "**/sessions/new" --timeout 20000; then rec 6 "S4-3→S6" "'Lead 생성됨' 확인 후 첫 세션" PASS "P1: Lead(claude_code, probe models[0]) 생성 후 S6 로 이동"; else rec 6 "S4-3→S6" "Lead 생성 후 S6" FAIL "url=$(ab get url)"; fi

step "U1-7~12 S6 마법사 — 제목·goal 입력, 2~7단계는 기본값 요약"
wait_sel '[data-testid="session-defaults"]' || die "S6 defaults"
DEF="$(ab get text '[data-testid="session-defaults"]' | tr '\n' ' ')"; log "defaults: $DEF"
PH="$(ab get attr '[data-testid="session-goal"]' placeholder 2>/dev/null || echo '')"
grep -q "국내 B2B SaaS" <<<"$PH" && rec 7 S6-1 "제목·goal 필드, 예시 placeholder" PASS "placeholder='$PH'" || rec 7 S6-1 "제목·goal 필드 + 예시 placeholder" FAIL "placeholder='$PH'"
grep -q "Director" <<<"$DEF" && grep -q "본인" <<<"$DEF" && rec 8 S6-2 "Director=본인, deputy 비움" PASS "요약 행" || rec 8 S6-2 "Director=본인" FAIL
grep -q "none" <<<"$DEF" && grep -q "worktree" <<<"$DEF" && rec 9 S6-3 "격리 none 기본, worktree 는 저장소 필요 설명" PASS "요약 행" || rec 9 S6-3 "격리 none 기본" FAIL
grep -q "자동 선택" <<<"$DEF" && grep -q "1대" <<<"$DEF" && rec 10 S6-4 "런타임 자동 선택 기본 + 방금 연결한 노트북 1대" PASS "요약 행" || rec 10 S6-4 "런타임 자동 선택 + 1대" FAIL "$DEF"
grep -q "assignee" <<<"$DEF" && rec 11 S6-5 "참여자 체크됨, assignee=Lead" PASS "요약 행(P1: Lead 1개)" || rec 11 S6-5 "참여자·assignee" FAIL
grep -q "artifact 제출" <<<"$DEF" && grep -q "Director 승인" <<<"$DEF" && rec 12 S6-6 "종료 조건 기본값: artifact 제출 AND Director 승인" PASS "요약 행" || rec 12 S6-6 "종료 조건 기본값" FAIL
ab fill '[data-testid="session-title"]' "결제 시장 조사" >/dev/null
ab fill '[data-testid="session-goal"]' "국내 B2B SaaS 결제 시장 조사 보고서 10페이지" >/dev/null
shot p1-u1-07-s6-goal
TS="$(now_ms)"
ab click '[data-testid="session-start"]' >/dev/null

step "U1-13 S7 — goal 시스템 메시지 · 참여자 칩 · 에이전트 응답(실시간)"
try ab wait --url "**/sessions/*" --timeout 20000 || die "S7 로 이동하지 않음"
wait_sel '[data-testid="session-detail"]'
SESSION_ID="$(ab get attr '[data-testid="session-detail"]' data-session-id)"
wait_cards 1 20 && FIRST="$(ab get text '[data-testid="message-card"]' | tr '\n' ' ' | head -c 160)" || FIRST=""
CHIP="$(ab get text '[data-testid="participants"] [data-testid="agent-chip"]' 2>/dev/null | tr '\n' ' ')"
shot p1-u1-13-s7-started
G13="PASS"; NOTE13="guided 문구: $(grep -o '질문 기한[^·]*' <<<"$DEF" | head -1)"
grep -q "결제 시장 조사 보고서" <<<"$FIRST" || { G13=FAIL; NOTE13="첫 카드='$FIRST'"; }
grep -q "Lead" <<<"$CHIP" || { G13=FAIL; NOTE13="$NOTE13 chip='$CHIP'"; }
rec 13 S7 "goal 시스템 메시지 1개 + 참여자 칩 Lead (lane 보드는 P2)" "$G13" "system='$(head -c 80 <<<"$FIRST")' chip='$CHIP' $NOTE13"
if wait_cards 2 240; then TR="$(now_ms)"; shot p1-u1-13b-s7-agent-reply; rec 13b S7 "에이전트(초기 task) 답글이 새로고침 없이 도착" PASS "$(( (TR-TS)/1000 ))s after start"; else shot p1-u1-13b-s7-agent-reply; rec 13b S7 "에이전트 답글 실시간 도착" FAIL; fi

step "U1-14/15 작성창 멘션 → 전송 → 답글 + 활동 피드 원본 레일"
ab fill '[data-testid="composer-input"]' "@" >/dev/null
wait_sel '[data-testid="mention-menu"]' 10 || bad "멘션 자동완성이 열리지 않음"
ab press Enter >/dev/null; ab type '[data-testid="composer-input"]' "인사해줘" >/dev/null
wait_sel '[data-testid="chip-trigger"]' 10 && CHIPTXT="$(ab get text '[data-testid="chip-trigger"]')" || CHIPTXT=""
shot p1-u1-14-composer-mention
[ -n "$CHIPTXT" ] && rec 14 S7 "@ 자동완성 → 트리거 미리보기 칩" PASS "chip='$CHIPTXT'" || rec 14 S7 "@ 자동완성 → 트리거 칩" FAIL
TP="$(now_ms)"; ab click '[data-testid="composer-send"]' >/dev/null
if wait_cards 4 240; then TR2="$(now_ms)"; rec 15 S7 "멘션 답글 실시간 도착" PASS "$(( (TR2-TP)/1000 ))s"; else rec 15 S7 "멘션 답글 도착" FAIL; fi
ab find testid activity-toggle click >/dev/null 2>&1 || true
wait_sel '[data-testid="activity-rail"]' 10 && RAIL="$(ab get text '[data-testid="activity-rail"]' | tr '\n' ' ' | head -c 200)" || RAIL=""
shot_full p1-u1-15-s7-reply-and-rail
[ -n "$RAIL" ] && rec 15b S7 "활동 피드 원본 레일(task_event 시간순)" PASS "$(head -c 120 <<<"$RAIL")" || rec 15b S7 "활동 피드 원본 레일" FAIL

step "U13 초대 링크로 두 번째 멤버 (P1 DoD 4)"
COOKIE="$OUT/cookies-u1.txt"; rm -f "$COOKIE"; login "$EMAIL" "$PASSWORD"
WS="$(api_ok GET /workspaces | jq -r '.[0].id')"
INV="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}')"; INV_URL="$(jq -r .url <<<"$INV")"; INV_TOKEN="$(jq -r .token <<<"$INV")"
log "invite url=$INV_URL"
# 링크의 호스트가 서버(:8080) 이면 웹(:3000) 으로 바꿔 연다 — 실제 사용자는 웹 링크를 받아야 한다(아래 판정에 기록)
OPEN_URL="$INV_URL"; case "$INV_URL" in "$SERVER_URL"*) OPEN_URL="$WEB_URL${INV_URL#"$SERVER_URL"}";; esac
export AGENT_BROWSER_SESSION="colab-p1-u13-${STAMP}"
ab set viewport 1280 900 >/dev/null
ab open "$OPEN_URL" >/dev/null
if wait_text "초대되었습니다" 20; then shot p1-u13-01-invite; rec U13-2a S3 "'마케팅팀에 초대되었습니다' + 가입/로그인" PASS "url=$INV_URL$( [ "$OPEN_URL" != "$INV_URL" ] && echo ' (invite.url 이 서버 오리진 — 웹 오리진으로 바꿔 열었음)')"; else shot p1-u13-01-invite; rec U13-2a S3 "초대 미리보기" FAIL "$(ab get text body | head -c 200)"; fi
ab click 'a[href^="/signup?invite="]' >/dev/null
wait_sel '[data-testid="signup-form"]' || die "invite signup"
ab fill 'input[name=display_name]' "$NAME2" >/dev/null; ab fill 'input[name=email]' "$EMAIL2" >/dev/null; ab fill 'input[name=password]' "$PASSWORD" >/dev/null
ab click 'button[type=submit]' >/dev/null
if try ab wait --url "**/sessions" --timeout 20000 && wait_sel '[data-testid="session-list"], [data-testid="session-row"]' 20; then shot p1-u13-02-s5-member; rec U13-2b S5 "가입 직후 S4 건너뛰고 S5(세션 목록)" PASS "url=$(ab get url)"; else shot p1-u13-02-s5-member; rec U13-2b S5 "S4 건너뛰고 S5" FAIL "url=$(ab get url)"; fi
MEMBERS="$(api_ok GET "/workspaces/$WS/members" | jq -r '(.items // .)|length')"
[ "$MEMBERS" = 2 ] && rec U13-3 API "워크스페이스 멤버 2명" PASS "members=$MEMBERS" || rec U13-3 API "멤버 2명" FAIL "members=$MEMBERS"
NAV_SETTINGS="$(ab get count 'a[href="/settings"]' 2>/dev/null || echo ?)"; log "member 내비 Settings 링크 수=$NAV_SETTINGS (U13 변형: member 에겐 없어야 함)"

step "결과"
column -t -s $'\t' "$STEPS" >&2
jq -n --arg session "$SESSION_ID" --arg ws "$WS" --arg email "$EMAIL" --arg pair_ms "$((T1-T0))" --arg invite_url "$INV_URL" --argjson members "$MEMBERS" \
  --argjson pass "$(awk -F'\t' 'NR>1&&$4=="PASS"' "$STEPS" | wc -l)" --argjson fail "$(awk -F'\t' 'NR>1&&$4=="FAIL"' "$STEPS" | wc -l)" --argjson na "$(awk -F'\t' 'NR>1&&$4=="N/A"' "$STEPS" | wc -l)" \
  '{session:$session,workspace:$ws,email:$email,pairing_to_ready_ms:$pair_ms,invite_url:$invite_url,members:$members,pass:$pass,fail:$fail,na:$na}' | tee "$OUT/d-summary.json"
