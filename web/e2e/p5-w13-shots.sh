#!/usr/bin/env bash
# T-W13 스크린샷 — S5 세션 카드 「…」 옵션 + 삭제(FR-2.7 · SCREEN §4.3·§5). `next build && next start` 로 찍는다(PR #186 NN5 —
# 개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다(PR #188 NN3).
#
#   p5-w13-01-menu-{light,dark}.png            카드 「…」 메뉴 열림 — 끝난 세션(활성 「삭제」)
#   p5-w13-02-menu-blocked-{light,dark}.png    진행 중 세션 — 「삭제」 비활성 + 사유 "진행 중인 미션은 먼저 종료하세요"
#   p5-w13-03-dialog-{light,dark}.png          확인 다이얼로그 — 제목에 세션 이름 · 사라지는 것 · 되돌릴 수 없음 · 위험 색 「삭제」
#   p5-w13-04-workdirs-{light,dark}.png        409 workdir_unmerged — 다이얼로그 안에 작업 폴더 목록(경로·브랜치·사유) + 「작업 폴더 관리」 링크
#   p5-w13-05-deleted-{light,dark}.png         204 뒤 — 카드가 빠지고 안내 한 줄(W-14: 어두움도)
#   p5-w13-06-menu-member-light.png            멤버 계정 — 남의 끝난 세션의 「삭제」 비활성 + 사유 "Director 나 소유자·관리자만 …"
#   p5-w13-07-s7-deleted-{light,dark}.png      S7 을 보는 중에 그 세션이 지워짐(session.deleted) → 목록으로 돌아와 안내 한 줄(W-14 — 흐름 검증,
#                                              `ab wait` 가 단언이다)
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/p5-w13-shots.sh
# ⚠ 개발 서버와 포트·빌드를 분리할 것(PR #191 NN5). SHOT_DIR 로 저장 위치를 바꿀 수 있다(기본 __screenshots__).
#
# ── 은퇴(T-R4, 2026-09-24) ──
# 이 스크립트는 옛 S5 세션 카드의 「삭제」(deleteSession)와 SSE `session.deleted` 를 몰던 기록이다. 옛 세션 화면은
# R1.5b 에서, op·이벤트·주소는 v0.3.0(R4, openapi D22)에서 지워져 더는 돌 수 없다. 마지막 본문은 git 에 있다: git show 41b10ba:web/e2e/p5-w13-shots.sh
# 대체: 방 「…」 메뉴·방 삭제 = web/e2e/r2-w1-shots.sh(04 메뉴) · lib/mock/rooms-mock.test.ts(deleteRoom 409·204·room.deleted).
echo "SKIP — 은퇴한 스크립트(deleteSession 삭제, T-R4). 대체: web/e2e/r2-w1-shots.sh · lib/mock/rooms-mock.test.ts" >&2
exit 0
