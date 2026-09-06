package acp_test

import (
	"fmt"
	"os"
)

func os_stat(p string) (os.FileInfo, error) { return os.Stat(p) }

// caseNameP3 keeps the P3a golden's subtest names byte-identical to the
// server-side table this file carries over (§0-8: expectations unchanged).
func caseNameP3(eval, name string) string {
	out := make([]byte, 0, len(eval))
	for i := 0; i < len(eval); i++ {
		if eval[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, eval[i])
	}
	return fmt.Sprintf("%s_%s", string(out), name)
}
