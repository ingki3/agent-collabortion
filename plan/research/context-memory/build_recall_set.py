#!/usr/bin/env python3
"""T-CTX0 회상 문항 v1 빌더 — 게임 제작 방(사본)에서 에이전트가 다시 알아야 했던 사실 30개.

문항마다 정답 근거는 **원문 조각(snippet)** 으로 적는다: 근거 메시지(또는 결정) 의 content/detail 에
그대로 있는 문자열. 빌더는
  1) 짧은 id(앞 8자리)를 전체 id 로 풀고,
  2) 조각이 정말 그 근거 안에 있는지 확인하고(없으면 실패),
  3) 지금 방식의 턴 프롬프트(Lead 의 최근 턴 — TestReplayContextMetrics 가 뽑은 brief+prompt)에
     그 조각이 실리는지를 규칙(①최근 50 / ②미션 전부 / [7]·③ 결정 / detail 400자 미리보기)으로 판정해
  recall-set-v1.json 을 쓴다.

사용: python3 build_recall_set.py MSG_JSON DEC_JSON BRIEF_TXT PROMPT_TXT OUT_JSON
"""
import json, re, sys

MSGS, DECS, BRIEF, PROMPT, OUT = sys.argv[1:6]
PREVIEW = 400  # bundle.go historyDetailPreview

# (id, type, question, answer, [(evidence_prefix, snippet), ...], judge)
#   evidence_prefix: 메시지 id 앞 8자리, 결정이면 "d:" + 앞 8자리
#   judge: "text" = 조각 포함이면 답할 수 있다(결정적) · "agent" = 조각은 있어도 답을 짜 맞춰야 한다(에이전트 채점)
Q = [
 # ── 현재 값 ────────────────────────────────────────────────────────────────
 ("Q01", "current_value", "마리오 카트 게임(PIXEL GRAND PRIX)의 레이스 BGM 현재 템포와 파이널 랩 템포는?",
  "레이스곡 152 BPM(v7 재편곡, 144→152), 파이널 랩은 같은 곡을 172 BPM 으로 가속",
  [("9853aa36", "144 → 152BPM"), ("84e717f3", "152BPM")], "text"),
 ("Q02", "current_value", "이니셜 D 게임에서 차간 충돌·물리에 쓰는 현재 차체 크기(유닛)는? (Designer 첫 스펙 53.8×21 은 대체됨)",
  "36 × 14 유닛(Researcher 권고, Lead 가 채택 — 카트 시절 58×43 을 대체)",
  [("d7e8f79c", "36 × 14 유닛"), ("c68bea02", "차는 36×14 입니다")], "text"),
 ("Q03", "current_value", "드리프트 최소 진입 속도 driftMinSpeed 의 현재 값은?",
  "V×0.55 (72 km/h) 유지 — 0.40V 로 내리지 않음",
  [("d:6e9aaf15", "driftMinSpeed 는 V×0.55(72 km/h) 를 유지한다")], "text"),
 ("Q04", "current_value", "도랑 흡착 gutPull 의 현재 값은?",
  "17 (더 올리지 않음)",
  [("d:a92d4611", "도랑 흡착 gutPull = 17 유지")], "text"),
 ("Q05", "current_value", "과속 스크럽(over) 문턱의 현재 값은? (v28 도입 때 값과 구분)",
  "1.9 (v28 에서 1.6 으로 도입 → v30 에서 1.9 로 올림, 체감 기능으로만 남김)",
  [("d:209edd9a", "문턱만 1.6 → 1.9 로 올리고")], "text"),
 ("Q06", "current_value", "코치 brake 졸업 조건의 apexOK 현재 값은?",
  "1.00 (1.15 에서 조임, !c.wall 삭제)",
  [("d:21ff8efb", "`apexOK` 를 1.15 → 1.00 으로 조인다")], "text"),
 ("Q07", "current_value", "이니셜 D 아티팩트 midnight-touge.html 의 최신 버전은(사본 시점)?",
  "v31",
  [("d65c2da1", "v31 제출했습니다")], "text"),
 # ── 결정과 이유 ────────────────────────────────────────────────────────────
 ("Q08", "decision_reason", "이니셜 D 게임의 엔딩을 별도 화면으로 만들지 않은 이유는?",
  "하루나는 매일 밤 다시 달리는 곳이라 '게임이 닫힌다'보다 '오늘 밤은 끝났다'가 맞아서 — 선택 화면 제목 한 줄(금색 '하루나에 남은 상대가 없다.')로",
  [("3538df2e", "하루나는 매일 밤 다시 달리는 곳이라"), ("d:8de9273c", "이 게임의 엔딩은 별도 화면이 아니라 선택 화면 제목 한 줄이다")], "agent"),
 ("Q09", "decision_reason", "코너 과속에 세금을 매기는 over 계열 손잡이를 통째로 닫은 근거 측정은?",
  "ohard=1 로 과속을 물리적으로 불가능하게 잘라도 지배 전략이 48/48 승·44초 — 44초가 계열 전체의 천장이라 고를 계수가 없음",
  [("1866b032", "`ohard=1` 이 이 건을 끝냈습니다"), ("5d7b7931", "**48/48 승 44.0초**")], "agent"),
 ("Q10", "decision_reason", "도랑 타기를 2번 상대(아사히)부터 쓸 수 있는 기술로 둔 이유는?",
  "코스·밸런스를 안 건드리고 가이드에 명시 — 사다리를 기술 하나 때문에 흔들 이유가 없다",
  [("f8c287f8", "사다리를 기술 하나 때문에 흔들 이유가 없다는 판단에 동의합니다"), ("d:49e7a5bc", "도랑 타기는 2번 상대(아사히)부터 쓸 수 있는 기술로 둔다")], "agent"),
 ("Q11", "decision_reason", "이니셜 D 에서 레퍼런스 사진을 찾았는데 게임 화면에 넣지 않은 이유(방식)는?",
  "사진은 팀이 보고 작업하는 레퍼런스로만 쓰고 전부 코드로 다시 그림(외부 파일 0), 원작 고유명사도 안 씀",
  [("9938919d", "사진을 게임에 넣지는 않았습니다")], "agent"),
 ("Q12", "decision_reason", "?sim 이 난이도 대리 측정으로 무효였던 이유는?",
  "트레일 브레이킹과 코너 클램프 ×1.22 가 !G.autopilot 게이트라 오토파일럿 플레이어에게 꺼져 있었음 → G.simHuman 으로 고침",
  [("d:4d5c958b", "`?sim` 은 유효한 난이도 대리 측정이 아니었다"), ("5ae5bcc1", "`?sim` 은 플레이어를 두 군데서 불구로 만듭니다")], "text"),
 # ── 누가 무엇을 ────────────────────────────────────────────────────────────
 ("Q13", "owner", "마리오 카트 게임의 BGM 시스템(WebAudio 합성)은 누가 만들었나?",
  "Developer",
  [("88db477f", "BGM 시스템 드롭인 코드는 detail 안의 JS 블록 하나입니다")], "text"),
 ("Q14", "owner", "드리프트 방향 판정에 쓴 필름스트립 하네스는 누가 만들었나?",
  "Developer (developer-35e27f04/film-d01ba678/)",
  [("ceb92d53", "드리프트 필름스트립 하네스를 만들었습니다")], "text"),
 ("Q15", "owner", "모바일에 브레이크 버튼이 없다는 결함을 찾은 사람은?",
  "Writer (가이드 v5 소스 대조 중)",
  [("44935773", "모바일에는 브레이크 버튼이 없습니다")], "text"),
 ("Q16", "owner", "이니셜 D 게임에 엔딩이 없다는 것을 찾은 사람과 방법은?",
  "Writer — 사다리 감사(touge-ladderwalk.md)에서 세 판을 실제로 이어 달려 확인",
  [("08fd15ef", "엔딩이 코드에 없습니다")], "agent"),
 # ── 포기한 안 ─────────────────────────────────────────────────────────────
 ("Q17", "abandoned", "드리프트 방향 전환에 쓰던 '0.18초 전환 타이머(driftFlip)'를 버린 이유는?",
  "카운터스티어와 전환이 같은 키라 시간 임계 하나로 구분이 안 됨 — 필름스트립에서 무페인트 진입 회귀(1.00→0.54/0.75)",
  [("1f8e80b7", "0.18초 시간 규칙은 삭제했고"), ("13149110", "카운터스티어와 전환이 같은 키")], "agent"),
 ("Q18", "abandoned", "마리오 카트 차 디자인에서 v10 의 클로즈드바디 스포츠카를 버린 이유는?",
  "Director 레퍼런스(MK64)는 오픈 고카트에 큰 캐릭터가 올라앉은 모양 — 스포츠카로 가면 캐릭터가 유리 안으로 사라져 정반대. v11 에서 MK64 형으로",
  [("3ff9fdd6", "두 방향은 정반대입니다"), ("7a5b54e1", "레퍼런스대로 다시 그렸습니다")], "agent"),
 ("Q19", "abandoned", "'던져넣기'(헤어핀에 각을 미리 세워 던져 넣는 입력)는 왜 넣지 않았나?",
  "창이 좁은 게 아니라 이 입력에 이득이 없어서 — 경로 곡률을 자르는 건 aCap 이고 드리프트는 ×1.28 곱뿐",
  [("d:152005a8", "'던져넣기'(헤어핀에 각을 미리 세워 던져 넣는 입력)는 기능으로 넣지 않는다"), ("bf4af96d", "이 입력에 이득이 없다")], "agent"),
 ("Q20", "abandoned", "레일 지배 전략 대책으로 넣었다가 철회한 railYield 의 값과 철회 이유는?",
  "railYield 1.4(1.2초 래치) — 지배 전략 효과 0(144/144 즉시결착승, 37.8↔38.1초), 변수는 맞고 재는 자리가 틀림 → 매 프레임 과속 스크럽으로 교체",
  [("d:9502583b", "v25 의 `railYield`(레일 접촉 프레임에서 재는 게이트)를 철회하고"), ("2f22a595", "변수는 맞았고 재는 자리가 틀렸습니다")], "agent"),
 ("Q21", "abandoned", "마리오 카트 게임 이름 후보 중 채택된 것과 버려진 것은?",
  "채택 PIXEL GRAND PRIX(Designer 추천), 버림 TURBO KART 7 · DRIFT DASH!",
  [("a8352fae", "PIXEL GRAND PRIX(추천) 2) TURBO KART 7 3) DRIFT DASH!"), ("3bb1a5fb", "이름 **PIXEL GRAND PRIX**")], "text"),
 # ── 파일·아티팩트 위치 ─────────────────────────────────────────────────────
 ("Q22", "artifact_location", "이니셜 D 레퍼런스 이미지(AE86 사진)는 어디에 있나?",
  "미션 공용 폴더 _shared/ref/ (위키미디어 커먼즈 4장)",
  [("a566bc89", "`_shared/ref/` 에 넣었고"), ("9938919d", "`_shared/ref/` 에 넣고")], "text"),
 ("Q23", "artifact_location", "디렉터가 들을 수 있게 렌더한 사운드 WAV 파일은 어디에 있나?",
  "_shared/audio/ (16-bit/44.1 kHz, README 맨 위 안내)",
  [("bfd1ebdb", "사운드 WAV 7개를 `_shared/audio/` 에"), ("6257ccf4", "_shared/audio/")], "text"),
 ("Q24", "artifact_location", "도랑 타기·블라인드 어택 모듈 파일 이름과 위치는?",
  "_shared/mech.js",
  [("9117f525", "`_shared/mech.js`"), ("af188617", "`_shared/mech.js`")], "text"),
 ("Q25", "artifact_location", "Researcher 의 이니셜 D 물리 조사 보고서(엔진 상수 표·물컵 판정식) 파일은?",
  "_shared/touge-research.md",
  [("d7e8f79c", "`_shared/touge-research.md`")], "text"),
 # ── 오래전(최근 50 밖) 사실 ────────────────────────────────────────────────
 ("Q26", "long_ago", "마리오 카트에서 Z 키(아이템)가 안 먹던 원인은?",
  "한글 IME 가 켜져 있으면 e.key 가 'ㅋ'/'Process' — 물리 키 e.code 를 같이 읽게 고침(W·A·S·D·M·R 도 같은 원인)",
  [("d4f2ff5e", "원인은 한글 IME 였습니다"), ("dfbf848e", "한글 IME 가 켜져 있으면")], "text"),
 ("Q27", "long_ago", "게임이 열을 많이 내던 원인과 고친 방법은?",
  "rAF 마다 무조건 렌더 — 120Hz 디스플레이에서 초당 120번, 프레임 예산 초과로 CPU 가 쉬지 못함 → 프레임 거버너(v13, 이니셜 D 는 v3 에 다시 넣음)",
  [("e8cc8e87", "프레임 거버너를 넣었습니다"), ("46cfc5fa", "거버너를 다시 넣고")], "agent"),
 ("Q28", "long_ago", "Director 가 알려 준 자신의 마리오 카트 베스트 랩과, 그때 AI 베스트 랩은?",
  "Director 19.36초(메시지엔 '19:36'), AI 28.14초 — 랩당 9초 차",
  [("ab4e1618", "내 Best lab 은 19:36 이야"), ("becbc23a", "AI 베스트랩이 28.14초")], "text"),
 ("Q29", "long_ago", "마리오 카트 캐릭터를 최고속도로 차별화하면 안 된다는 근거는?",
  "속도 지각의 Weber 분수 5~7% — 3랩 70초에서 최고속 5% 차이는 3.5초라 느껴지는 최소치가 밸런스 붕괴점보다 위. 최고속은 4% 스프레드로 묶고 가속·미니터보로 차별화",
  [("3131ffed", "**최고속도로 차별화하면 안 됩니다.**")], "agent"),
 ("Q30", "long_ago", "마리오 카트 트랙 길이를 늘린 수치는?",
  "9,072 → 13,416 유닛(+48%)",
  [("d4f2ff5e", "**트랙 9,072 → 13,416유닛** (+48%)")], "text"),
]

msgs = {m["id"]: m for m in json.load(open(MSGS))}
decs = {d["id"]: d for d in json.load(open(DECS))}
brief, prompt = open(BRIEF).read(), open(PROMPT).read()


def norm(s):
    return re.sub(r"\s+", " ", s).strip()


def resolve(prefix):
    if prefix.startswith("d:"):
        hits = [k for k in decs if k.startswith(prefix[2:])]
        kind = "decision"
    else:
        hits = [k for k in msgs if k.startswith(prefix)]
        kind = "message"
    if len(hits) != 1:
        raise SystemExit(f"id {prefix}: {len(hits)} matches")
    return kind, hits[0]


# 지금 방식의 턴 프롬프트: ① <history> 의 메시지 id, ② <mission_messages> 의 id, [7]/③ 은 결정 전부(≤20+나머지)
hist_block = prompt[prompt.index("<history"):prompt.index("</history>")]
mission_block = prompt[prompt.index("<mission_messages"):prompt.index("</mission_messages>")] if "<mission_messages" in prompt else ""
ctx = norm(brief + "\n" + prompt)
out = []
errors = []
for qid, typ, q, a, evs, judge in Q:
    ev_out = []
    for prefix, snip in evs:
        kind, full = resolve(prefix)
        if kind == "decision":
            d = decs[full]
            where = "summary" if norm(snip) in norm(d["summary"]) else "rationale" if norm(snip) in norm(d.get("rationale") or "") else None
            bundle = "brief.7_or_room_decisions"
            rule_in = where == "summary"  # [7]·③ 은 summary 전부 + rationale 160자 미리보기
        else:
            m = msgs[full]
            content, detail = m["content"] or "", m["detail"] or ""
            where = "content" if norm(snip) in norm(content) else None
            if where is None and norm(snip) in norm(detail):
                pos = detail.find(snip) if snip in detail else norm(detail).find(norm(snip))
                where = "detail[:400]" if pos + len(snip) <= PREVIEW else "detail[400:]"
            bundle = "history" if full in hist_block else "mission_messages" if full in mission_block else "absent"
            rule_in = bundle != "absent" and where in ("content", "detail[:400]")
        if where is None:
            errors.append(f"{qid}: snippet not in {prefix}: {snip!r}")
        ev_out.append({"kind": kind, "id": full, "snippet": snip, "where": where, "bundle": bundle,
                       "in_prompt_rule": rule_in, "in_prompt_text": norm(snip) in ctx})
    out.append({"id": qid, "type": typ, "question": q, "answer": a, "judge": judge, "evidence": ev_out,
                "in_current_prompt": any(e["in_prompt_text"] for e in ev_out)})
if errors:
    print("\n".join(errors), file=sys.stderr)
    raise SystemExit(1)
doc = {
    "version": 1,
    "room": "02aa7e66-b326-4def-830d-ee13f2248770",
    "snapshot": "live-snapshot5.dump (2026-09-28 09:39 KST)",
    "baseline_turn": {"agent": "Lead", "task": "2520a00d-0764-4b33-8852-86f2c35dd7c6",
                      "note": "사본의 Lead 마지막 attempt 를 bundle.go 로 재생한 brief+prompt (TestReplayContextMetrics)"},
    "grading": {
        "text": "근거 조각 중 하나라도 맥락 텍스트에 (공백 정규화 후) 그대로 있으면 1 — 결정적",
        "agent": "조각 포함은 필요조건. 맥락만 읽고 정답을 짜 맞출 수 있는지 에이전트가 판정해 agent_grade 에 기록",
    },
    "questions": out,
}
json.dump(doc, open(OUT, "w"), ensure_ascii=False, indent=1)
print(f"{len(out)} questions → {OUT}")
