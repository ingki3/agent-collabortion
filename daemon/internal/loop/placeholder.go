package loop

import (
	"regexp"
	"sort"
	"strings"

	"github.com/ingki3/agent-collabortion/contracts"
)

// placeholder.go implements the harness §10 v0.8.7 placeholder substitution.
//
// The server writes the brief and the turn prompt, but some of what those
// texts have to say is a path only THIS daemon knows: the workdir root is a
// per-machine setting, and `rebind_prepare` (daemon-protocol §4.3) drops a
// rebound session's artifacts under it. So the server writes a placeholder
// and the daemon swaps in the absolute path — the same division of labour as
// the cli_wrapper rewrite (§10 v0.8.1), at the same place and in the same
// order: wrapper rewrite → placeholder substitution → pointer line.
//
// A placeholder we do not know is NOT passed through. `{{COLAB_…}}` reaching
// an agent is a path that does not exist, and an agent told to read a
// non-existent diff either invents the contents or fails halfway through the
// turn — after the money is spent. The attempt fails as `config` before the
// runtime starts, and the placeholder's own name goes in the feed so whoever
// wrote that text can see which one we could not fill.

// PlaceholderRebindDir is the §10 v1 list — one entry. It resolves to
// `<workdir_root>/.colab/rebind/<session_id>` (RebindDir), the directory
// `rebind_prepare` downloads into.
const PlaceholderRebindDir = "{{COLAB_REBIND_DIR}}"

// placeholderRe matches any `{{COLAB_*}}` token, known or not — the leftover
// check has to see the ones this daemon has never heard of, which are exactly
// the dangerous ones (a newer server talking to an older daemon).
var placeholderRe = regexp.MustCompile(`\{\{COLAB_[A-Z0-9_]*\}\}`)

// placeholderValues is the substitution table for one attempt.
func (d *Daemon) placeholderValues(b contracts.TaskBundle) map[string]string {
	return map[string]string{
		PlaceholderRebindDir: RebindDir(d.Cfg.WorkdirRoot, b.Task.SessionID),
	}
}

// substitutePlaceholders replaces every known placeholder in text. Unknown
// ones are left standing on purpose so leftoverPlaceholders can name them.
func substitutePlaceholders(text string, vals map[string]string) string {
	if text == "" {
		return text
	}
	for name, v := range vals {
		if v == "" {
			// No value is not a substitution: an empty replacement would
			// hand the agent a bare "/…" or a relative path.
			continue
		}
		text = strings.ReplaceAll(text, name, v)
	}
	return text
}

// leftoverPlaceholders returns the distinct `{{COLAB_…}}` tokens still
// standing in texts, sorted, for the failure detail (§10 v0.8.7).
func leftoverPlaceholders(texts ...string) []string {
	seen := map[string]bool{}
	for _, t := range texts {
		for _, m := range placeholderRe.FindAllString(t, -1) {
			seen[m] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
