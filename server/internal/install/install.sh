#!/bin/sh
# colab-daemon installer — served by the Colab server itself at
# @@COLAB_SERVER_URL@@/install.sh (daemon-protocol / openapi `Pairing.install_commands`).
#
# 이 스크립트가 하는 일은 셋뿐이다:
#   1. 필요한 도구(go·git)가 있는지 확인하고, 없으면 사람이 읽을 수 있는 안내로 멈춘다.
#   2. 데몬을 소스에서 빌드해 사용자 영역(기본 ~/.colab/bin)에 `colab-daemon` 으로 놓는다.
#   3. 그 디렉터리를 PATH 에 올린다(로그인 셸 프로파일에 표시된 블록 한 개, 멱등).
#
# 시스템 디렉터리를 건드리지 않는다: sudo 를 쓰지 않고, /usr/local 에 아무것도 쓰지 않으며,
# 빌드는 임시 디렉터리에서 하고 끝나면 지운다.
set -eu

# 서버가 자기 오리진을 그대로 심는다. 이 파일에 하드코딩된 호스트는 없다 — 값은
# 이 스크립트를 서비스한 서버의 COLAB_SERVER_URL 이다(다른 배포를 가리키면 페어링이 어긋난다).
COLAB_SERVER_URL="${COLAB_SERVER_URL:-@@COLAB_SERVER_URL@@}"

COLAB_HOME="${COLAB_HOME:-$HOME/.colab}"
BIN_DIR="$COLAB_HOME/bin"
BIN_NAME="colab-daemon"
REPO_URL="${COLAB_INSTALL_REPO:-https://github.com/ingki3/agent-collabortion.git}"
REPO_REF="${COLAB_INSTALL_REF:-}"

say()  { printf '%s\n' "$*"; }
step() { printf '\033[36m▶\033[0m %s\n' "$*"; }
die()  { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

say "colab-daemon 설치 — 서버 $COLAB_SERVER_URL"

# ---------------------------------------------------------------------------
# 0. 배포 아티팩트 분기가 들어올 자리
# ---------------------------------------------------------------------------
# 아직 릴리스 바이너리가 없다. 생기면 여기서 uname -s/-m 으로 이름을 만들어
#   curl -fsSL "$COLAB_SERVER_URL/dist/colab-daemon_${os}_${arch}" -o "$tmp/$BIN_NAME"
# 를 시도하고, 200 이 아니거나 체크섬이 어긋나면 아래 소스 빌드로 떨어진다.
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
    say "  • Go 1.25 이상: https://go.dev/dl/ 에서 받거나"
    say "      macOS  brew install go"
    say "      Ubuntu/Debian  sudo apt install golang-go   (1.25 미만이면 go.dev/dl 을 쓰세요)"
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
say "  go  $(go version 2>/dev/null | awk '{print $3}')"
say "  git $(git --version 2>/dev/null | awk '{print $3}')"

# ---------------------------------------------------------------------------
# 2. 소스에서 빌드 (임시 디렉터리, 끝나면 삭제)
# ---------------------------------------------------------------------------
work="$(mktemp -d "${TMPDIR:-/tmp}/colab-install.XXXXXX")"
trap 'rm -rf "$work"' EXIT INT TERM

step "소스 받기 ($REPO_URL${REPO_REF:+ @ $REPO_REF})"
if [ -n "$REPO_REF" ]; then
  git clone --quiet --depth 1 --branch "$REPO_REF" "$REPO_URL" "$work/src" \
    || die "저장소를 받지 못했습니다: $REPO_URL ($REPO_REF)"
else
  git clone --quiet --depth 1 "$REPO_URL" "$work/src" \
    || die "저장소를 받지 못했습니다: $REPO_URL"
fi

step "데몬 빌드"
# GOWORK=off: 저장소 루트의 go.work 는 server·cli 까지 묶고 있어 데몬 하나를 세우는 데
# 필요 없는 의존성을 전부 끌어온다. 데몬 모듈은 contracts 를 replace 로만 참조한다.
( cd "$work/src/daemon" && GOWORK=off go build -o "$work/$BIN_NAME" ./cmd/daemon ) \
  || die "빌드에 실패했습니다. go 버전이 1.25 이상인지 확인하세요: $(go version 2>/dev/null)"

# ---------------------------------------------------------------------------
# 3. 사용자 영역에 설치
# ---------------------------------------------------------------------------
step "설치 $BIN_DIR/$BIN_NAME"
mkdir -p "$BIN_DIR"
# 실행 중인 바이너리를 덮어쓰면 "text file busy" 가 날 수 있어 mv 로 바꿔 끼운다.
mv -f "$work/$BIN_NAME" "$BIN_DIR/$BIN_NAME"
chmod +x "$BIN_DIR/$BIN_NAME"

# ---------------------------------------------------------------------------
# 4. PATH — 표시된 블록 하나, 멱등
# ---------------------------------------------------------------------------
profile=""
case "${SHELL:-}" in
  */zsh)  profile="$HOME/.zshrc" ;;
  */bash) if [ -f "$HOME/.bashrc" ]; then profile="$HOME/.bashrc"; else profile="$HOME/.bash_profile"; fi ;;
  *)      profile="$HOME/.profile" ;;
esac
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
    say "  PATH 추가: $profile"
  fi
fi

say ""
say "설치 완료: $("$BIN_DIR/$BIN_NAME" version 2>/dev/null || echo "$BIN_DIR/$BIN_NAME")"
say ""
if [ "$on_path" -eq 0 ]; then
  say "지금 이 셸에서 바로 쓰려면:"
  say "  export PATH=\"$BIN_DIR:\$PATH\""
  say ""
fi
say "다음 단계 — 화면의 둘째 줄(페어링 코드가 채워진 명령)을 그대로 붙여넣으세요:"
say "  colab-daemon pair <pairing_token> --server $COLAB_SERVER_URL"
