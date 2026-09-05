package acp_test

import (
	"os"
	"testing"

	"github.com/ingki3/agent-collabortion/daemon/internal/acpfake"
)

func TestMain(m *testing.M) {
	acpfake.MaybeMain()
	os.Exit(m.Run())
}
