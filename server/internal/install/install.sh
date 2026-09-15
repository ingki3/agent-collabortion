#!/bin/sh
# colab-daemon installer — served by the Colab server itself at
# @@COLAB_SERVER_URL@@/install.sh (daemon-protocol / openapi `Pairing.install_commands`).
#
# 이 스크립트가 하는 일은 셋뿐이다:
#   1. 필요한 도구(go·git)가 있는지 확인하고, 없으면 사람이 읽을 수 있는 안내로 멈춘다.
#   2. 데몬과 colab CLI 를 소스에서 빌드해 사용자 영역(기본 ~/.colab/bin)에 `colab-daemon`·`colab` 으로 놓는다.
#   3. 그 디렉터리를 PATH 에 올린다(로그인 셸 프로파일에 표시된 블록 한 개, 멱등).
#
# 왜 CLI 도 같이 놓는가(Lead 판정 2026-09-08). 에이전트가 플랫폼에 말하는 수단은 이 바이너리 하나다
# (colab-cli.md §1 — 데몬이 등록하는 MCP 서버와 셸 경로가 같은 실행 파일이다). 데몬만 놓으면 첫 probe 가
# `colab_cli.present=false` 로 뜨고 세션은 조용히 아무 일도 못 하는 상태가 된다 — G8 이 재는 F1 이 바로
# 거기서 막힌다.
#
# 시스템 디렉터리를 건드리지 않는다: sudo 를 쓰지 않고, /usr/local 에 아무것도 쓰지 않으며,
# 빌드는 임시 디렉터리에서 하고 끝나면 지운다.
set -eu

# 서버가 자기 오리진을 그대로 심는다. 이 파일에 하드코딩된 호스트는 없다 — 값은
# 이 스크립트를 서비스한 서버의 COLAB_SERVER_URL 이다(다른 배포를 가리키면 페어링이 어긋난다).
COLAB_SERVER_URL="${COLAB_SERVER_URL:-@@COLAB_SERVER_URL@@}"

COLAB_HOME="${COLAB_HOME:-$HOME/.colab}"
BIN_DIR="$COLAB_HOME/bin"
DAEMON_NAME="colab-daemon"
CLI_NAME="colab"
REPO_URL="${COLAB_INSTALL_REPO:-https://github.com/ingki3/agent-collabortion.git}"
# 서버가 자기 빌드 커밋(또는 태그)을 심는다(S-64). 참가자 전원이 **서버와 같은 커밋**을 받아야
# 하므로 기본값은 main 이 아니라 이 값이다. 비어 있으면 서버가 자기 커밋을 모르는 빌드다 —
# 아래에서 그 사실을 말하고 저장소 기본 브랜치로 떨어진다.
REPO_REF="${COLAB_INSTALL_REF:-@@COLAB_INSTALL_REF@@}"
# go.work 의 go 버전 — 소스 빌드에 필요한 최소 버전(S-65). 저장소를 받기 전에 확인하므로
# 서버가 자기 go.work 에서 읽어 심는다(숫자를 여기 또 적으면 갈라진다).
GO_MIN="@@COLAB_GO_MIN@@"

say()  { printf '%s\n' "$*"; }
step() { printf '\033[36m▶\033[0m %s\n' "$*"; }
die()  { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

say "colab 설치 — 데몬(colab-daemon)과 CLI(colab), 서버 $COLAB_SERVER_URL"

# ---------------------------------------------------------------------------
# 0. 배포 아티팩트 분기가 들어올 자리
# ---------------------------------------------------------------------------
# 아직 릴리스 바이너리가 없다. 생기면 여기서 uname -s/-m 으로 이름을 만들어
#   curl -fsSL "$COLAB_SERVER_URL/dist/colab-daemon_${os}_${arch}" -o "$tmp/$DAEMON_NAME"
# (colab CLI 도 같은 모양으로) 를 시도하고, 200 이 아니거나 체크섬이 어긋나면 아래 소스 빌드로 떨어진다.
# 그때까지는 소스 빌드가 기본 경로다(go 가 필요한 이유).

# ---------------------------------------------------------------------------
# 1. 도구 확인 — 없으면 안내하고 멈춘다(반쯤 설치된 상태를 남기지 않는다)
# ---------------------------------------------------------------------------
step "필요한 도구 확인"
missing=""
command -v git >/dev/null 2>&1 || missing="$missing git"
command -v go  >/dev/null 2>&1 || missing="$missing go"
if [ -n "$missing" ]; then
  say ""
  say "설치를 계속할 수 없습니다 — 다음이 필요합니다:$missing"
  say ""
  case "$missing" in *go*)
    say "  • Go ${GO_MIN:-1.25} 이상: https://go.dev/dl/ 에서 받거나"
    say "      macOS  brew install go"
    say "      Ubuntu/Debian  sudo apt install golang-go   (${GO_MIN:-1.25} 미만이면 go.dev/dl 을 쓰세요)"
    ;;
  esac
  case "$missing" in *git*)
    say "  • git: macOS 는 xcode-select --install, 리눅스는 배포판 패키지 관리자"
    ;;
  esac
  say ""
  say "설치한 뒤 같은 명령을 다시 실행하세요:"
  say "  curl -fsSL $COLAB_SERVER_URL/install.sh | sh"
  exit 1
fi
GO_HAVE="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
say "  go  $GO_HAVE"
say "  git $(git --version 2>/dev/null | awk '{print $3}')"
# 버전 비교(S-65): 있는지만 보면 1.21 에서 빌드가 깨진 뒤에야 안다. `sort -V` 는 BSD 에 없으므로
# 숫자 세 자리를 직접 견준다.
ver_ge() { # ver_ge 1.25.1 1.25.0 → 0(참)
  a1=$(printf '%s' "$1" | cut -d. -f1); a2=$(printf '%s' "$1" | cut -d. -f2); a3=$(printf '%s' "$1" | cut -d. -f3)
  b1=$(printf '%s' "$2" | cut -d. -f1); b2=$(printf '%s' "$2" | cut -d. -f2); b3=$(printf '%s' "$2" | cut -d. -f3)
  a1=${a1:-0}; a2=${a2:-0}; a3=${a3:-0}; b1=${b1:-0}; b2=${b2:-0}; b3=${b3:-0}
  # rc·beta 접미사는 잘라 낸다(1.26rc1 → 1.26)
  a2=${a2%%[!0-9]*}; a3=${a3%%[!0-9]*}; b2=${b2%%[!0-9]*}; b3=${b3%%[!0-9]*}
  a2=${a2:-0}; a3=${a3:-0}; b2=${b2:-0}; b3=${b3:-0}
  [ "$a1" -gt "$b1" ] && return 0; [ "$a1" -lt "$b1" ] && return 1
  [ "$a2" -gt "$b2" ] && return 0; [ "$a2" -lt "$b2" ] && return 1
  [ "$a3" -ge "$b3" ]
}
if [ -n "$GO_MIN" ] && ! ver_ge "${GO_HAVE:-0}" "$GO_MIN"; then
  say ""
  say "설치를 계속할 수 없습니다 — go $GO_MIN 이상이 필요한데 $GO_HAVE 입니다."
  say "  https://go.dev/dl/ 에서 받거나   macOS  brew install go"
  say ""
  say "설치한 뒤 같은 명령을 다시 실행하세요:"
  say "  curl -fsSL $COLAB_SERVER_URL/install.sh | sh"
  exit 1
fi

# ---------------------------------------------------------------------------
# 2. 소스에서 빌드 (임시 디렉터리, 끝나면 삭제)
# ---------------------------------------------------------------------------
work="$(mktemp -d "${TMPDIR:-/tmp}/colab-install.XXXXXX")"
trap 'rm -rf "$work"' EXIT INT TERM

if [ -n "$REPO_REF" ]; then
  step "소스 받기 ($REPO_URL @ $REPO_REF — 서버와 같은 커밋)"
  # 태그·브랜치면 --branch 로 얕게, 커밋이면 그 sha 를 얕게 fetch(GitHub·file:// 모두 됨),
  # 둘 다 안 되는 오래된 git 이면 전체를 받아 checkout — 어느 길로 가든 결과는 같은 커밋이다.
  if ! git clone --quiet --depth 1 --branch "$REPO_REF" "$REPO_URL" "$work/src" 2>/dev/null; then
    rm -rf "$work/src"
    if git init --quiet "$work/src" \
       && git -C "$work/src" remote add origin "$REPO_URL" \
       && git -C "$work/src" fetch --quiet --depth 1 origin "$REPO_REF" 2>/dev/null \
       && git -C "$work/src" checkout --quiet FETCH_HEAD 2>/dev/null; then :
    else
      rm -rf "$work/src"
      git clone --quiet "$REPO_URL" "$work/src" \
        && git -C "$work/src" checkout --quiet "$REPO_REF" \
        || die "저장소를 받지 못했습니다: $REPO_URL ($REPO_REF)"
    fi
  fi
else
  step "소스 받기 ($REPO_URL — 서버가 자기 커밋을 모르는 빌드라 기본 브랜치)"
  git clone --quiet --depth 1 "$REPO_URL" "$work/src" \
    || die "저장소를 받지 못했습니다: $REPO_URL"
fi
SRC_COMMIT="$(git -C "$work/src" rev-parse HEAD 2>/dev/null || echo unknown)"
say "  커밋 $SRC_COMMIT"

step "데몬 빌드"
# GOWORK=off: 저장소 루트의 go.work 는 server 까지 묶고 있어 필요 없는 의존성을 전부 끌어온다.
# daemon·cli 모듈은 contracts 를 replace 로만 참조하므로 각자 따로 빌드된다.
# 데몬 버전 = 빌드한 커밋(S-64). probe 가 이 값을 daemon_version 으로 광고하고 S11 카드가 보여
# 주므로, 서버와 다른 커밋의 데몬은 눈에 보인다(서버는 다르면 로그만 남긴다 — 계약상 대조 칸 없음).
( cd "$work/src/daemon" && GOWORK=off go build -ldflags "-X main.version=$SRC_COMMIT" -o "$work/$DAEMON_NAME" ./cmd/daemon ) \
  || die "빌드에 실패했습니다. go 버전이 ${GO_MIN:-1.25} 이상인지 확인하세요: $(go version 2>/dev/null)"

step "colab CLI 빌드"
# 버전은 저장소 Makefile 의 COLAB_VERSION 을 그대로 쓴다(여기에 숫자를 또 적으면 둘이 갈라진다).
# probe 는 `colab --version` 의 첫 x.y.z 를 colab_cli.version 으로 읽는다(§3, 백로그 C-3).
CLI_VERSION="$(sed -n 's/^COLAB_VERSION[[:space:]]*?=[[:space:]]*\([0-9][^[:space:]]*\).*/\1/p' "$work/src/Makefile" 2>/dev/null | head -1)"
if [ -n "${CLI_VERSION:-}" ]; then
  ( cd "$work/src/cli" && GOWORK=off go build -ldflags "-X main.version=$CLI_VERSION" -o "$work/$CLI_NAME" ./cmd/colab ) \
    || die "colab CLI 빌드에 실패했습니다."
else
  ( cd "$work/src/cli" && GOWORK=off go build -o "$work/$CLI_NAME" ./cmd/colab ) \
    || die "colab CLI 빌드에 실패했습니다."
fi

# ---------------------------------------------------------------------------
# 3. 사용자 영역에 설치
# ---------------------------------------------------------------------------
step "설치 $BIN_DIR/{$DAEMON_NAME,$CLI_NAME}"
mkdir -p "$BIN_DIR"
# 실행 중인 바이너리를 덮어쓰면 "text file busy" 가 날 수 있어 mv 로 바꿔 끼운다.
for b in "$DAEMON_NAME" "$CLI_NAME"; do
  mv -f "$work/$b" "$BIN_DIR/$b"
  chmod +x "$BIN_DIR/$b"
done

# ---------------------------------------------------------------------------
# 4. PATH — 표시된 블록 하나, 멱등
# ---------------------------------------------------------------------------
profile=""
case "${SHELL:-}" in
  */zsh)  profile="$HOME/.zshrc" ;;
  */bash) if [ -f "$HOME/.bashrc" ]; then profile="$HOME/.bashrc"; else profile="$HOME/.bash_profile"; fi ;;
  *)      profile="$HOME/.profile" ;;
esac
# 마커 문자열은 바꾸지 않는다: 이미 설치한 사람의 프로파일에 같은 문자열이 들어 있고,
# 이름을 바꾸면 grep -qF 가 어긋나 블록이 두 개가 된다(멱등이 깨진다).
marker="# >>> colab-daemon >>>"
case ":${PATH}:" in
  *":$BIN_DIR:"*) on_path=1 ;;
  *)              on_path=0 ;;
esac
if [ "$on_path" -eq 0 ] && [ "${COLAB_INSTALL_NO_PROFILE:-0}" != "1" ]; then
  if [ ! -f "$profile" ] || ! grep -qF "$marker" "$profile" 2>/dev/null; then
    {
      printf '\n%s\n' "$marker"
      printf 'export PATH="%s:$PATH"\n' "$BIN_DIR"
      printf '%s\n' "# <<< colab-daemon <<<"
    } >> "$profile"
    say "  PATH 추가: $profile — 새 셸에서 colab-daemon·colab 둘 다 쓸 수 있습니다"
  fi
fi

say ""
say "설치 완료:"
say "  $("$BIN_DIR/$DAEMON_NAME" version 2>/dev/null || echo "$BIN_DIR/$DAEMON_NAME")"
say "  $("$BIN_DIR/$CLI_NAME" --version 2>/dev/null | head -1 || echo "$BIN_DIR/$CLI_NAME")"
say ""
if [ "$on_path" -eq 0 ]; then
  say "지금 이 셸에서 바로 쓰려면(두 바이너리 모두 이 한 줄로 잡힙니다):"
  say "  export PATH=\"$BIN_DIR:\$PATH\""
  say ""
fi
# 왜 PATH 가 데몬만의 문제가 아닌가: 데몬은 probe 에서 `colab` 을 PATH 로 찾고
# (daemon-protocol §3 colab_cli), 에이전트도 셸에서 같은 이름으로 부른다. 이 디렉터리가
# PATH 에 없으면 데몬은 떠 있는데 첫 probe 가 colab_cli.present=false 로 뜬다.
say "다음 단계 — 화면의 둘째 줄(페어링 코드가 채워진 명령)을 그대로 붙여넣으세요:"
say "  colab-daemon pair <pairing_token> --server $COLAB_SERVER_URL"
say ""
say "페어링하면 데몬이 첫 probe 를 보냅니다. 설치가 제대로 됐는지는 그 probe 의"
say "colab_cli.present 로 확인하세요 — false 면 위 PATH 줄이 빠진 것입니다."
