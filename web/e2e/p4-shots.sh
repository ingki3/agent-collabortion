#!/usr/bin/env bash
# T-W5 스크린샷 — 목 API 위에서 찍는다(에이전트 턴은 목이 흉내 낸다).
#
#
# ── 은퇴(T-R2-W4b, 2026-09-24) ──
# v0.19 에서 S6 마법사(/sessions/new)가 지워졌고(→ /rooms/new 307) 옛 S7(/sessions/:id)은 방 화면(/rooms/:id)으로 바뀌었다.
# 이 스크립트는 그 두 화면을 끝까지 몰던 기록이라 새 화면에서는 돌 수 없다. 판정 근거는 plan/ 의 게이트 보고서에 남아 있고,
# 마지막 본문은 git 에 있다: git show e45db1e:web/e2e/p4-shots.sh
# 대체: 방 흐름 U1 = web/e2e/u1.sh · 시나리오 A~D·S7 = e2e/p5/72_~78_(CI) · 미션 편집·조건 고치기 화면 = web/e2e/r2-w4b-shots.sh.
echo "SKIP — 은퇴한 스크립트(S6 마법사 삭제, T-R2-W4b). 대체: web/e2e/u1.sh · e2e/p5/72_~78_ · web/e2e/r2-w4b-shots.sh" >&2
exit 0
