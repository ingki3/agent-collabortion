#!/usr/bin/env bash
# plan/spikes/spike05/gitprobe.sh — skip-worktree 의 git 부수 효과를 런타임 없이 전수로 잰다.
# 런타임 매트릭스(run_all.sh)가 재는 것은 "런타임이 읽는가 / 에이전트가 편집·커밋하면"이고,
# 이 스크립트는 그 뒤에 오는 git 명령들(add·commit·stash·checkout·merge·pull·worktree)이
# skip-worktree 파일에 어떻게 반응하는지를 잰다. 출력은 사람이 읽는 로그다.
set -u
W="${SPIKE05_WORK:-/private/tmp/colab-spike05}/gitprobe"
rm -rf "$W"; mkdir -p "$W"
M_START='<!-- colab:brief:start -->'
M_END='<!-- colab:brief:end -->'
say() { printf '\n### %s\n' "$*"; }
g()   { local o rc; printf '$ git -C %s %s\n' "${1##*/}" "${*:2}"; o=$(git -C "$@" 2>&1); rc=$?; printf '%s\n' "$o" | sed 's/^/    /'; printf '    (rc=%s)\n' "$rc"; }

mkrepo() {
  local root=$1
  mkdir -p "$root"; git -C "$root" init -q -b main
  git -C "$root" config user.email s05@example.invalid; git -C "$root" config user.name S5
  printf '# Widget Catalog\n' > "$root/README.md"
  git -C "$root" add -A; git -C "$root" commit -qm "chore: init"
  printf '# rules\n\n- PROJECT_RULE_CODE = ORIG-RULE-4471\n' > "$root/CLAUDE.md"
  git -C "$root" add -A; git -C "$root" commit -qm "docs: rules"
  printf '# Widgets\n\n- alpha\n' > "$root/widgets.md"
  git -C "$root" add -A; git -C "$root" commit -qm "feat: widgets"
}
mark() { printf '\n%s\nBRIEF BODY\n%s\n' "$M_START" "$M_END" >> "$1"; }
unmark() { python3 - "$1" <<'PY'
import sys
p=sys.argv[1]; b=open(p).read()
s=b.find('<!-- colab:brief:start -->'); e=b.find('<!-- colab:brief:end -->')
if s>=0 and e>=0:
    end=e+len('<!-- colab:brief:end -->')
    if end<len(b) and b[end]=='\n': end+=1
    if s>=2 and b[s-1]=='\n' and b[s-2]=='\n': s-=1
    open(p,'w').write(b[:s]+b[end:])
PY
}

# ---------------------------------------------------------------- P1
say "P1  skip-worktree 를 세운 뒤 append 하면 status·diff 가 무엇을 보이는가"
A="$W/p1"; mkrepo "$A"
mark "$A/CLAUDE.md"; git -C "$A" update-index --skip-worktree CLAUDE.md
g "$A" ls-files -v CLAUDE.md
g "$A" status --porcelain
g "$A" diff --stat
g "$A" diff --stat HEAD

# ---------------------------------------------------------------- P2
say "P2  에이전트가 같은 파일을 편집하고 커밋하려 하면 (add -A / add <file> / commit -a)"
printf '<!-- agent-note: NOTE-1 -->\n' >> "$A/CLAUDE.md"
g "$A" status --short
g "$A" add -A
g "$A" status --short
g "$A" add CLAUDE.md
g "$A" commit -m "docs: agent note"
g "$A" commit -a -m "docs: agent note (commit -a)"
g "$A" log --oneline -2
echo "    파일에 노트가 남아 있는가: $(grep -c 'agent-note' "$A/CLAUDE.md")"

say "P2b  강제로 스테이징하는 우회 (--no-skip-worktree 없이): git update-index --add / add --force / stash"
g "$A" add --force CLAUDE.md
g "$A" stash push -m probe
g "$A" status --short
echo "    stash 뒤 파일에 마커가 남아 있는가: $(grep -c 'colab:brief:start' "$A/CLAUDE.md")"

# ---------------------------------------------------------------- P3
say "P3  브랜치 이동·병합·리셋이 skip-worktree 파일을 만나면"
B="$W/p3"; mkrepo "$B"
git -C "$B" branch -q other
git -C "$B" switch -q other >/dev/null 2>&1
printf -- '- another rule from the repo side\n' >> "$B/CLAUDE.md"
git -C "$B" commit -qam "docs: upstream edit to CLAUDE.md"
git -C "$B" switch -q main >/dev/null 2>&1
mark "$B/CLAUDE.md"; git -C "$B" update-index --skip-worktree CLAUDE.md
printf '<!-- agent-note: NOTE-2 -->\n' >> "$B/CLAUDE.md"
g "$B" switch other
g "$B" merge other -m "merge other"
echo "    merge 뒤 마커가 남아 있는가: $(grep -c 'colab:brief:start' "$B/CLAUDE.md")  노트: $(grep -c 'agent-note' "$B/CLAUDE.md")"
g "$B" status --short
g "$B" log --oneline -2
g "$B" reset --hard HEAD
echo "    reset --hard 뒤 마커: $(grep -c 'colab:brief:start' "$B/CLAUDE.md")  노트: $(grep -c 'agent-note' "$B/CLAUDE.md")"

# ---------------------------------------------------------------- P4
say "P4  worktree 의 index 는 별개인가 (skip-worktree 가 worktree 마다인가)"
C="$W/p4"; mkrepo "$C"
git -C "$C" worktree add -q -b colab/sess1/agentA "$C/../p4-wt"
WT="$W/p4-wt"
mark "$WT/CLAUDE.md"; git -C "$WT" update-index --skip-worktree CLAUDE.md
echo "-- worktree 쪽 --"; g "$WT" ls-files -v CLAUDE.md; g "$WT" status --porcelain
echo "-- 원본 체크아웃 쪽 --"; g "$C" ls-files -v CLAUDE.md; g "$C" status --porcelain
echo "    index 파일 위치: $(git -C "$WT" rev-parse --git-dir)/index vs $(git -C "$C" rev-parse --git-dir)/index"
echo "-- 원본 쪽에서 같은 파일을 편집하면 worktree 의 S 비트가 영향을 받는가 --"
printf -- '- main-side edit\n' >> "$C/CLAUDE.md"
g "$C" status --porcelain
g "$WT" ls-files -v CLAUDE.md
echo "-- worktree 를 지우면(gc) index 도 함께 사라지는가 --"
git -C "$C" checkout -q -- CLAUDE.md
g "$C" worktree remove --force "$WT"
g "$C" worktree list

# ---------------------------------------------------------------- P5
say "P5  복원: --no-skip-worktree → 마커 구간만 제거 (에이전트 편집 유무 두 갈래)"
for variant in noedit edited; do
  D="$W/p5-$variant"; mkrepo "$D"
  mark "$D/CLAUDE.md"; git -C "$D" update-index --skip-worktree CLAUDE.md
  [ "$variant" = edited ] && printf '<!-- agent-note: NOTE-3 -->\n' >> "$D/CLAUDE.md"
  git -C "$D" update-index --no-skip-worktree CLAUDE.md
  unmark "$D/CLAUDE.md"
  echo "-- $variant --"
  g "$D" status --porcelain
  g "$D" diff -- CLAUDE.md
  echo "    원본 규칙 살아 있는가: $(grep -c 'ORIG-RULE-4471' "$D/CLAUDE.md")  마커 잔여: $(grep -c 'colab:brief' "$D/CLAUDE.md")"
done

say "P6  복원 전에 데몬이 죽으면 (S 비트가 남은 채 파일에 마커가 남는다)"
E="$W/p6"; mkrepo "$E"
mark "$E/CLAUDE.md"; git -C "$E" update-index --skip-worktree CLAUDE.md
g "$E" status --porcelain
echo "    사람이 clone 을 새로 받으면 마커는 없다(작업 트리 로컬 상태). 같은 체크아웃에서는:"
g "$E" ls-files -v CLAUDE.md
echo "    → 다음 세션이 같은 workdir 를 다시 쓰면 append 가 중복되는가는 brief.writeMarkerBlock 이 strip 후 append 라 1개 유지"

say "환경"
git --version

# ---------------------------------------------------------------- P7
say "P7  시나리오 B 의 실제 모양: 코드 파일 + 지시 파일을 같이 고치고 커밋한다"
F="$W/p7"; mkrepo "$F"
mark "$F/CLAUDE.md"; git -C "$F" update-index --skip-worktree CLAUDE.md
printf -- '- beta\n' >> "$F/widgets.md"
printf '<!-- agent-note: NOTE-7 -->\n' >> "$F/CLAUDE.md"
g "$F" status --short
g "$F" add -A
g "$F" commit -m "feat: widget + rule note"
g "$F" show --stat --oneline HEAD
g "$F" status --short
echo "    → 코드 파일은 커밋됐고 지시 파일 편집은 조용히 빠졌다. 에이전트가 보는 status 는 클린."
echo "    커밋에 CLAUDE.md 가 있는가: $(git -C "$F" show --name-only --format= HEAD | grep -c CLAUDE.md)"
echo "    파일에 노트가 남아 있는가: $(grep -c 'agent-note' "$F/CLAUDE.md")"
echo "    HEAD:CLAUDE.md 에 마커가 섞였는가: $(git -C "$F" show HEAD:CLAUDE.md | grep -c 'colab:brief')"

say "P8  복원 후 에이전트 편집을 데몬이 커밋할 수 있는가 (P4 가 원한다면)"
git -C "$F" update-index --no-skip-worktree CLAUDE.md
unmark "$F/CLAUDE.md"
g "$F" status --short
g "$F" diff --stat
echo "    → 복원 뒤에는 평범한 modified 라 사람도 에이전트도 커밋할 수 있다(단, 그 턴은 이미 끝났다)."
