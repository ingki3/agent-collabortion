package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ingki3/agent-collabortion/daemon/internal/acpprobe"
)

// ---- spike 1: claude-code-acp maturity -------------------------------------
//
// N turns that each need a permission (Write/Edit/Bash in "default" mode),
// R process restarts + session/load, C cancelled turns. Measures crashes,
// resume success, allow_once absence.

func (r *runner) spike1(ctx context.Context) error {
	type turnRec struct {
		I           int    `json:"i"`
		Kind        string `json:"kind"` // work | cancel_text | cancel_perm | resume_probe
		StopReason  string `json:"stopReason"`
		ToolCalls   int    `json:"toolCalls"`
		Permissions int    `json:"permissions"`
		Ms          int64  `json:"ms"`
		Err         string `json:"err,omitempty"`
		Text        string `json:"text,omitempty"`
	}
	var (
		turns     []turnRec
		resumes   []map[string]any
		resumeOK  int
		cancelOK  int
		cancelled int
	)
	c, _, err := r.spawn(ctx, "spike1")
	if err != nil {
		return err
	}
	var sid string
	if r.o.session != "" {
		// Continue a previous run's session (rate-limit interruption): the
		// first resume of this run is the load itself.
		r.retire(c)
		var err error
		c, sid, err = r.respawnAndLoad(ctx, r.o.session, &resumes, &resumeOK, "continue_previous_run")
		if err != nil {
			return err
		}
	} else {
		s, err := r.newSession(ctx, c, r.claudeMeta())
		if err != nil {
			return err
		}
		sid = s.SessionID
	}
	r.summary["session_id"] = sid

	// Every prompt must need a permission in "default" mode. Claude Code
	// auto-approves read-only shell (ls, wc, date, sleep …) and remembers a
	// file once its write was approved, so each turn touches a NEW path via a
	// mutating tool (Write / Bash redirection / mkdir / cp).
	prompts := []string{
		"Create a file named note_%d.txt in the current directory containing the single line 'probe %d'. Use the Write tool. Then reply 'done'.",
		"Run `mkdir -p dir_%d && echo made > dir_%d/made.txt` using the Bash tool, then reply 'done'.",
		"Run `cp note_%d.txt copy_%d.txt` using the Bash tool (create note_%d.txt with Bash first if it is missing), then reply 'done'.",
		"Run `echo shell-%d > shell_%d.txt` using the Bash tool, then reply 'done'.",
		"Create a file named b_%d.json containing {\"probe\": %d} using the Write tool, then reply 'done'.",
	}
	total := r.o.turns
	resumeEvery := 0
	if r.o.resumes > 0 {
		resumeEvery = max(1, total/r.o.resumes)
	}
	cancelEvery := 0
	if r.o.cancels > 0 {
		cancelEvery = max(1, total/r.o.cancels)
	}
	resumesDone, cancelsDone := 0, 0

	for i := 1; i <= total; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// --- cancel turn (interleaved, does not count as a work turn) ---
		if cancelEvery > 0 && cancelsDone < r.o.cancels && i%cancelEvery == 0 {
			cancelsDone++
			kind := "cancel_text"
			prompt := "Count from 1 to 300 in words, one number per line. Do not use tools."
			if cancelsDone%2 == 0 {
				kind = "cancel_perm"
				prompt = "Run `python3 -c 'import time; time.sleep(45)' && echo slept > slept_%d.txt` using the Bash tool and then reply 'slept'."
			}
			prompt = strings.ReplaceAll(prompt, "%d", fmt.Sprint(i))
			r.logf("turn %d/%d %s", i, total, kind)
			done := make(chan struct{})
			var tr *acpprobe.TurnResult
			var terr error
			go func() { tr, terr = r.turn(ctx, c, sid, prompt); close(done) }()
			delay := 2500 * time.Millisecond
			if kind == "cancel_perm" {
				delay = 4 * time.Second // let the permission request arrive first
			}
			select {
			case <-time.After(delay):
				_ = c.Cancel(sid)
			case <-done:
			}
			<-done
			rec := turnRec{I: i, Kind: kind}
			if terr != nil {
				rec.Err = terr.Error()
				if errors.Is(terr, ErrRateLimited) {
					turns = append(turns, rec)
					r.summary["turns"] = turns
					return terr
				}
			} else {
				rec.StopReason, rec.ToolCalls, rec.Permissions, rec.Ms = tr.StopReason, tr.ToolCalls, tr.Permissions, tr.Duration.Milliseconds()
				cancelled++
				if tr.StopReason == "cancelled" {
					cancelOK++
				}
			}
			turns = append(turns, rec)
			if exited, _ := c.Exited(); exited {
				r.crashes++
				r.rec.Note("crash", map[string]any{"after_turn": i, "kind": kind})
				c, sid, err = r.respawnAndLoad(ctx, sid, &resumes, &resumeOK, "crash_recovery")
				if err != nil {
					return err
				}
			}
		}

		// --- work turn ---
		p := prompts[(i-1)%len(prompts)]
		prompt := strings.ReplaceAll(p, "%d", fmt.Sprint(i))
		r.logf("turn %d/%d work", i, total)
		tr, terr := r.turn(ctx, c, sid, prompt)
		rec := turnRec{I: i, Kind: "work"}
		if terr != nil {
			rec.Err = terr.Error()
			turns = append(turns, rec)
			if errors.Is(terr, ErrRateLimited) {
				r.summary["turns"] = turns
				return terr
			}
			if exited, _ := c.Exited(); exited {
				r.crashes++
				r.rec.Note("crash", map[string]any{"after_turn": i})
				c, sid, err = r.respawnAndLoad(ctx, sid, &resumes, &resumeOK, "crash_recovery")
				if err != nil {
					return err
				}
				continue
			}
		} else {
			rec.StopReason, rec.ToolCalls, rec.Permissions, rec.Ms = tr.StopReason, tr.ToolCalls, tr.Permissions, tr.Duration.Milliseconds()
			rec.Text = firstLine(tr.Text)
			turns = append(turns, rec)
		}

		// --- planned resume: kill process, new process, session/load ---
		if resumeEvery > 0 && resumesDone < r.o.resumes && i%resumeEvery == 0 {
			resumesDone++
			r.logf("resume %d/%d (session/load in fresh process)", resumesDone, r.o.resumes)
			r.retire(c)
			c, sid, err = r.respawnAndLoad(ctx, sid, &resumes, &resumeOK, "planned")
			if err != nil {
				return err
			}
		}
	}
	r.summary["turns"] = turns
	r.summary["work_turns"] = total
	r.summary["resumes"] = resumes
	r.summary["resume_attempts"] = len(resumes)
	r.summary["resume_ok"] = resumeOK
	r.summary["cancel_attempts"] = cancelled
	r.summary["cancel_ok"] = cancelOK
	r.summary["final_session_id"] = sid
	return nil
}

// respawnAndLoad starts a fresh process and session/load's sid; on failure it
// records the attempt and falls back to a new session (so the run continues).
func (r *runner) respawnAndLoad(ctx context.Context, sid string, resumes *[]map[string]any, ok *int, why string) (*acpprobe.Client, string, error) {
	c, _, err := r.spawn(ctx, "resume")
	if err != nil {
		return nil, "", err
	}
	replayed := 0
	c.OnUpdate(func(p acpprobe.SessionUpdateParams) {
		var k acpprobe.UpdateKind
		_ = json.Unmarshal(p.Update, &k)
		if k.SessionUpdate == "user_message_chunk" || k.SessionUpdate == "agent_message_chunk" {
			replayed++
		}
	})
	lctx, cancel := context.WithTimeout(ctx, r.o.timeout)
	defer cancel()
	start := time.Now()
	res, lerr := c.LoadSession(lctx, r.o.workdir, sid, r.claudeMeta())
	entry := map[string]any{"why": why, "sessionId": sid, "ms": time.Since(start).Milliseconds(), "replayed_chunks": replayed}
	if lerr == nil {
		if res != nil && res.Models != nil && r.o.model != "" {
			if id := pickModel(res.Models, r.o.model); id != "" {
				_ = c.SetModel(lctx, sid, id)
			}
		} else if res != nil && r.o.model != "" && len(res.ConfigOptions) > 0 {
			// claude-agent-acp 0.74.0: a resumed session can come back on the
			// default model even when session/new ran on another (SPIKE_01b E1).
			entry["model_after_load"] = acpprobe.ConfigOptionValue(res.ConfigOptions, "model")
			r.ensureModel(lctx, c, sid, res.ConfigOptions)
		}
		// Prove the session is usable: one tool-free turn.
		tr, terr := r.turn(ctx, c, sid, "Reply with exactly 'resumed'. Do not use tools.")
		if terr != nil {
			lerr = terr
			if errors.Is(terr, ErrRateLimited) {
				*resumes = append(*resumes, entry)
				return c, sid, terr
			}
		} else {
			entry["probe_stop"] = tr.StopReason
			entry["probe_text"] = firstLine(tr.Text)
			entry["probe_models"] = tr.Models
		}
	}
	if lerr != nil {
		entry["error"] = lerr.Error()
		*resumes = append(*resumes, entry)
		r.rec.Note("resume_failed", entry)
		// fall back: new session in this process so the run continues
		s, nerr := r.newSession(ctx, c, r.claudeMeta())
		if nerr != nil {
			return c, sid, nerr
		}
		return c, s.SessionID, nil
	}
	entry["ok"] = true
	*ok++
	*resumes = append(*resumes, entry)
	return c, sid, nil
}

// claudeMeta passes the model through _meta.claudeCode.options so it applies
// from the first turn (set_model is also called after session/new).
func (r *runner) claudeMeta() map[string]any {
	if r.o.runtime != "claude" || r.o.model == "" {
		return nil
	}
	return map[string]any{"claudeCode": map[string]any{"options": map[string]any{"model": r.o.model}}}
}

// ---- spike 2: blocking AskUserQuestion ---------------------------------------

func (r *runner) spike2(ctx context.Context) error {
	type exp struct {
		Name       string            `json:"name"`
		Setup      string            `json:"setup"`
		Prompt     string            `json:"prompt"`
		StopReason string            `json:"stopReason"`
		Text       string            `json:"text"`
		ToolCalls  []json.RawMessage `json:"toolCalls"`
		Perms      int               `json:"permissionRequests"`
		Err        string            `json:"err,omitempty"`
	}
	askPrompt := "You MUST call the AskUserQuestion tool right now to ask me which colour I prefer (options: red, blue). Do not answer the question yourself. If you cannot call that tool, reply with exactly 'TOOL_UNAVAILABLE' followed by a comma-separated list of the tool names you do have."
	datePrompt := "Run the shell command `date` using the Bash tool and reply with its output verbatim. If the tool call fails or is blocked, reply with exactly 'BLOCKED: ' followed by the error text you received, verbatim."

	run := func(name, setup string, meta map[string]any, prompt string) exp {
		e := exp{Name: name, Setup: setup, Prompt: prompt}
		r.rec.Note("spike2_experiment", map[string]any{"name": name, "setup": setup})
		c, _, err := r.spawn(ctx, "spike2:"+name)
		if err != nil {
			e.Err = err.Error()
			return e
		}
		defer r.retire(c)
		c.OnUpdate(func(p acpprobe.SessionUpdateParams) {
			var k acpprobe.UpdateKind
			_ = json.Unmarshal(p.Update, &k)
			if k.SessionUpdate == "tool_call" || k.SessionUpdate == "tool_call_update" {
				e.ToolCalls = append(e.ToolCalls, json.RawMessage(append([]byte(nil), p.Update...)))
			}
		})
		m := r.claudeMeta()
		for k, v := range meta {
			if m == nil {
				m = map[string]any{}
			}
			if k == "claudeCode" {
				// merge options
				opts := m["claudeCode"].(map[string]any)["options"].(map[string]any)
				for ok, ov := range v.(map[string]any)["options"].(map[string]any) {
					opts[ok] = ov
				}
				continue
			}
			m[k] = v
		}
		s, err := r.newSession(ctx, c, m)
		if err != nil {
			e.Err = err.Error()
			return e
		}
		tr, err := r.turn(ctx, c, s.SessionID, prompt)
		if err != nil {
			e.Err = err.Error()
			return e
		}
		e.StopReason, e.Text, e.Perms = tr.StopReason, tr.Text, tr.Permissions
		return e
	}

	var results []exp
	// (a) adapter default: claude-code-acp hard-codes disallowedTools=["AskUserQuestion"].
	results = append(results, run("a_adapter_default", "no settings; adapter's built-in disallowedTools", nil, askPrompt))
	if e := results[len(results)-1]; errors.Is(errors.New(e.Err), ErrRateLimited) || strings.Contains(e.Err, "rate_limited") {
		r.summary["experiments"] = results
		return ErrRateLimited
	}
	// (b) settings.json permissions.deny in the workdir (.claude/settings.json).
	settingsDir := filepath.Join(r.o.workdir, ".claude")
	_ = os.MkdirAll(settingsDir, 0o755)
	settingsPath := filepath.Join(settingsDir, "settings.json")
	_ = os.WriteFile(settingsPath, []byte(`{"permissions":{"deny":["Bash(date:*)","Bash(date)"]}}`), 0o644)
	results = append(results, run("b_settings_deny_bash_date", "workdir/.claude/settings.json permissions.deny=[Bash(date:*)]", nil, datePrompt))
	_ = os.Remove(settingsPath)
	// (c) _meta.claudeCode.options.disallowedTools (adapter passes SDK options through).
	results = append(results, run("c_meta_disallowedTools_bash", "_meta.claudeCode.options.disallowedTools=[Bash]", map[string]any{"claudeCode": map[string]any{"options": map[string]any{"disallowedTools": []string{"Bash"}}}}, datePrompt))
	// (d) control: nothing blocked → date should run after one permission request.
	results = append(results, run("d_control_bash_allowed", "nothing blocked", nil, datePrompt))
	r.summary["experiments"] = results
	for _, e := range results {
		if strings.Contains(e.Err, "rate_limited") {
			return ErrRateLimited
		}
	}
	return nil
}

// ---- spike 3: system prompt via session/new ---------------------------------

func (r *runner) spike3(ctx context.Context) error {
	type exp struct {
		Name   string         `json:"name"`
		Meta   map[string]any `json:"meta"`
		Prompt string         `json:"prompt"`
		Text   string         `json:"text"`
		Stop   string         `json:"stopReason"`
		Err    string         `json:"err,omitempty"`
	}
	brief := "Your name is Zorblax-7. You are a probe agent for the Colab project. When asked your name, answer with exactly 'Zorblax-7'. When asked your secret word, answer 'pomegranate'."
	q := "Two questions, answer on two lines and nothing else: 1) What is your name? 2) What is your secret word? Do not use tools."
	run := func(name string, meta map[string]any) exp {
		e := exp{Name: name, Meta: meta, Prompt: q}
		r.rec.Note("spike3_experiment", map[string]any{"name": name})
		c, _, err := r.spawn(ctx, "spike3:"+name)
		if err != nil {
			e.Err = err.Error()
			return e
		}
		defer r.retire(c)
		m := r.claudeMeta()
		for k, v := range meta {
			if m == nil {
				m = map[string]any{}
			}
			m[k] = v
		}
		s, err := r.newSession(ctx, c, m)
		if err != nil {
			e.Err = err.Error()
			return e
		}
		tr, err := r.turn(ctx, c, s.SessionID, q)
		if err != nil {
			e.Err = err.Error()
			return e
		}
		e.Text, e.Stop = tr.Text, tr.StopReason
		return e
	}
	var results []exp
	if r.o.runtime == "claude" {
		results = append(results, run("control_no_meta", nil))
		results = append(results, run("meta_systemPrompt_string_replace", map[string]any{"systemPrompt": brief}))
		results = append(results, run("meta_systemPrompt_append", map[string]any{"systemPrompt": map[string]any{"append": brief}}))
	} else {
		// Hermes: try the same keys; the ACP schema itself has no such field.
		results = append(results, run("control_no_meta", nil))
		results = append(results, run("meta_systemPrompt_string", map[string]any{"systemPrompt": brief}))
		results = append(results, run("top_level_systemPrompt", map[string]any{"hermes": map[string]any{"systemPrompt": brief}}))
	}
	r.summary["experiments"] = results
	for _, e := range results {
		if strings.Contains(e.Err, "rate_limited") {
			return ErrRateLimited
		}
	}
	return nil
}

// ---- spike 4a (a): context retention across process restart + session/load --

func (r *runner) spike4a(ctx context.Context) error {
	type rep struct {
		I         int    `json:"i"`
		SessionID string `json:"sessionId"`
		Keyword   string `json:"keyword"`
		Turn1     string `json:"turn1"`
		Turn1Stop string `json:"turn1Stop"`
		Replayed  int    `json:"replayedChunks"`
		Turn2     string `json:"turn2"`
		Turn2Stop string `json:"turn2Stop"`
		Retained  bool   `json:"retained"`
		Err       string `json:"err,omitempty"`
	}
	var reps []rep
	retained := 0
	words := []string{"pomegranate", "quasar", "lighthouse", "saffron", "meridian", "tungsten", "obsidian", "cinnamon", "harbinger", "zeppelin", "marzipan", "basalt"}
	for i := 1; i <= r.o.n; i++ {
		if ctx.Err() != nil {
			break
		}
		kw := fmt.Sprintf("%s-%d", words[(i-1)%len(words)], 1000+i*7)
		e := rep{I: i, Keyword: kw}
		r.logf("spike4a rep %d/%d keyword=%s", i, r.o.n, kw)
		c, _, err := r.spawn(ctx, fmt.Sprintf("spike4a:%d", i))
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			break
		}
		s, err := r.newSession(ctx, c, r.claudeMeta())
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			r.retire(c)
			break
		}
		e.SessionID = s.SessionID
		tr, err := r.turn(ctx, c, s.SessionID, fmt.Sprintf("Create a file named memo_%d.txt containing exactly the word '%s' (use the Write tool). Then reply 'saved'.", i, kw))
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			r.retire(c)
			if errors.Is(err, ErrRateLimited) {
				break
			}
			continue
		}
		e.Turn1, e.Turn1Stop = firstLine(tr.Text), tr.StopReason
		r.retire(c) // kill the process — the daemon-restart case

		c2, _, err := r.spawn(ctx, fmt.Sprintf("spike4a:%d:resume", i))
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			break
		}
		c2.OnUpdate(func(p acpprobe.SessionUpdateParams) {
			var k acpprobe.UpdateKind
			_ = json.Unmarshal(p.Update, &k)
			if k.SessionUpdate == "user_message_chunk" || k.SessionUpdate == "agent_message_chunk" {
				e.Replayed++
			}
		})
		lctx, cancel := context.WithTimeout(ctx, r.o.timeout)
		res, err := c2.LoadSession(lctx, r.o.workdir, s.SessionID, r.claudeMeta())
		cancel()
		if err != nil {
			e.Err = "load: " + err.Error()
			reps = append(reps, e)
			r.retire(c2)
			continue
		}
		if res != nil && res.Models != nil && r.o.model != "" {
			if id := pickModel(res.Models, r.o.model); id != "" {
				_ = c2.SetModel(ctx, s.SessionID, id)
			}
		}
		tr2, err := r.turn(ctx, c2, s.SessionID, "Without using any tools and without reading any files: what were you doing just before this message? Name the exact file and the exact word you wrote into it.")
		if err != nil {
			e.Err = "turn2: " + err.Error()
			reps = append(reps, e)
			r.retire(c2)
			if errors.Is(err, ErrRateLimited) {
				break
			}
			continue
		}
		e.Turn2, e.Turn2Stop = strings.TrimSpace(tr2.Text), tr2.StopReason
		e.Retained = strings.Contains(strings.ToLower(tr2.Text), strings.ToLower(kw))
		if e.Retained {
			retained++
		}
		reps = append(reps, e)
		r.retire(c2)
	}
	r.summary["reps"] = reps
	r.summary["retained"] = retained
	r.summary["attempted"] = len(reps)
	if r.o.n > 0 {
		r.summary["retention_rate"] = float64(retained) / float64(r.o.n)
	}
	for _, e := range reps {
		if strings.Contains(e.Err, "rate_limited") {
			return ErrRateLimited
		}
	}
	return nil
}

// ---- spike 4a (b): Hermes session loss detection ----------------------------
//
// new session → 1 turn → kill → delete the row from ~/.hermes/state.db →
// fresh process → session/resume (and session/load) → compare provenance.

func (r *runner) hermesLoss(ctx context.Context) error {
	type rep struct {
		I            int             `json:"i"`
		SessionID    string          `json:"sessionId"`
		NewMeta      json.RawMessage `json:"newSessionMeta"`
		Turn1        string          `json:"turn1"`
		DBRowBefore  string          `json:"dbRowBefore"`
		Deleted      bool            `json:"deleted"`
		DBRowAfter   string          `json:"dbRowAfter"`
		LoadErr      string          `json:"loadErr"`
		LoadMeta     json.RawMessage `json:"loadMeta"`
		ResumeErr    string          `json:"resumeErr"`
		ResumeMeta   json.RawMessage `json:"resumeMeta"`
		ResumeProvID string          `json:"resumeProvenanceAcpSessionId"`
		Turn2        string          `json:"turn2"`
		Turn2Stop    string          `json:"turn2Stop"`
		Turn2Tools   int             `json:"turn2Tools"`
		LossDetected bool            `json:"lossDetected"`
		DetectedBy   string          `json:"detectedBy"`
		Err          string          `json:"err,omitempty"`
	}
	db := filepath.Join(os.Getenv("HOME"), ".hermes", "state.db")
	if h := os.Getenv("HERMES_HOME"); h != "" {
		db = filepath.Join(h, "state.db")
	}
	r.summary["state_db"] = db
	sql := func(q string) string {
		out, err := exec.Command("sqlite3", db, q).CombinedOutput()
		if err != nil {
			return "ERR: " + err.Error() + " " + string(out)
		}
		return strings.TrimSpace(string(out))
	}
	var reps []rep
	detected := 0
	for i := 1; i <= r.o.n; i++ {
		e := rep{I: i}
		control := i == r.o.n && r.o.n > 1 // last rep: no deletion → provenance must match
		r.logf("hermes-loss rep %d/%d control=%v", i, r.o.n, control)
		c, _, err := r.spawn(ctx, fmt.Sprintf("hermes-loss:%d", i))
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			break
		}
		s, err := r.newSession(ctx, c, nil)
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			r.retire(c)
			break
		}
		e.SessionID, e.NewMeta = s.SessionID, s.Meta
		tr, err := r.turn(ctx, c, s.SessionID, fmt.Sprintf("Remember the code word 'walrus-%d'. Reply with exactly 'ok'. Do not use tools.", i))
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			r.retire(c)
			if errors.Is(err, ErrRateLimited) {
				break
			}
			continue
		}
		e.Turn1 = firstLine(tr.Text)
		r.retire(c)
		time.Sleep(500 * time.Millisecond) // let Hermes flush the DB write
		e.DBRowBefore = sql(fmt.Sprintf("select id, source, model, started_at, message_count from sessions where id='%s'", s.SessionID))
		if !control {
			sql(fmt.Sprintf("delete from messages where session_id='%s'", s.SessionID))
			sql(fmt.Sprintf("delete from sessions where id='%s' and source='acp'", s.SessionID))
			e.Deleted = true
		}
		e.DBRowAfter = sql(fmt.Sprintf("select id, source from sessions where id='%s'", s.SessionID))

		c2, _, err := r.spawn(ctx, fmt.Sprintf("hermes-loss:%d:resume", i))
		if err != nil {
			e.Err = err.Error()
			reps = append(reps, e)
			break
		}
		lctx, cancel := context.WithTimeout(ctx, r.o.timeout)
		lres, lerr := c2.LoadSession(lctx, r.o.workdir, s.SessionID, nil)
		if lerr != nil {
			e.LoadErr = lerr.Error()
		} else if lres != nil {
			e.LoadMeta = lres.Meta
		}
		rres, rerr := c2.ResumeSession(lctx, r.o.workdir, s.SessionID, nil)
		cancel()
		if rerr != nil {
			e.ResumeErr = rerr.Error()
		} else if rres != nil {
			e.ResumeMeta = rres.Meta
			var pm struct {
				Hermes struct {
					SessionProvenance struct {
						AcpSessionID string `json:"acpSessionId"`
					} `json:"sessionProvenance"`
				} `json:"hermes"`
			}
			_ = json.Unmarshal(rres.Meta, &pm)
			e.ResumeProvID = pm.Hermes.SessionProvenance.AcpSessionID
		}
		// Which session id is live now? If Hermes silently created a new one,
		// prompting the OLD id fails or answers without memory.
		promptSID := s.SessionID
		if e.ResumeProvID != "" && e.ResumeProvID != s.SessionID {
			promptSID = e.ResumeProvID
		}
		if rerr == nil {
			tr2, err := r.turn(ctx, c2, promptSID, "What was the code word I asked you to remember? Reply with the word only. Do not use tools.")
			if err != nil {
				e.Turn2 = "ERR: " + err.Error()
			} else {
				e.Turn2, e.Turn2Stop, e.Turn2Tools = strings.TrimSpace(tr2.Text), tr2.StopReason, tr2.ToolCalls
			}
		}
		r.retire(c2)
		// Detection rules (PRD §8.2.5): provenance mismatch, or load error, or refusal+0 activity.
		switch {
		case lerr != nil && rerr == nil && e.ResumeProvID != "" && e.ResumeProvID != s.SessionID:
			e.LossDetected, e.DetectedBy = true, "load_error+provenance_mismatch"
		case e.ResumeProvID != "" && e.ResumeProvID != s.SessionID:
			e.LossDetected, e.DetectedBy = true, "provenance_mismatch"
		case lerr != nil:
			e.LossDetected, e.DetectedBy = true, "load_error"
		case rerr != nil:
			e.LossDetected, e.DetectedBy = true, "resume_error"
		case e.Turn2Stop == "refusal" && e.Turn2Tools == 0:
			e.LossDetected, e.DetectedBy = true, "refusal_zero_activity"
		}
		if e.Deleted && e.LossDetected {
			detected++
		}
		if !e.Deleted {
			e.DetectedBy = "control:" + e.DetectedBy
		}
		reps = append(reps, e)
	}
	r.summary["reps"] = reps
	deletedRuns := 0
	for _, e := range reps {
		if e.Deleted {
			deletedRuns++
		}
	}
	r.summary["deleted_runs"] = deletedRuns
	r.summary["loss_detected"] = detected
	if deletedRuns > 0 {
		r.summary["detection_rate"] = float64(detected) / float64(deletedRuns)
	}
	return nil
}
