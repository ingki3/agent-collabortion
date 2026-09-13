package testchat

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
)

// FirstTurnPreamble is the one line §4.5 puts in front of the first user turn:
// "첫 턴은 서버가 「이것은 시험 대화다 — 플랫폼 명령은 쓸 수 없다」 한 줄을 앞에 붙인다".
// Agent-facing text (the turn prompt), not a screen sentence.
const FirstTurnPreamble = "이것은 시험 대화다 — 플랫폼 명령은 쓸 수 없다."

// WorkdirPath is the temporary directory §4.5 names for a chat:
// `<workdir_root>/.colab/testchat/<test_chat_id>`. The daemon deletes it on
// `gc` only when it is under `.colab/testchat/`, so the shape is part of the
// contract, not a convention.
func WorkdirPath(root string, id uuid.UUID) string {
	return path.Join(root, ".colab", "testchat", id.String())
}

// ClaimTurns hands out up to `slots` queued turns of chats fixed to runtimeID,
// inside the caller's claim transaction (daemon-protocol §4.5 "동시성·claim":
// a turn takes one `capacity` slot exactly like a session task). Each claimed
// turn moves queued → dispatched and comes back as a §4.5 bundle.
//
// It is called by queue.Postgres.Claim AFTER the session tasks, with whatever
// capacity they left — a test chat never displaces real work.
func ClaimTurns(ctx context.Context, tx pgx.Tx, runtimeID uuid.UUID, slots int, now time.Time) ([]contracts.TaskBundle, error) {
	if slots <= 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM test_chat
		WHERE runtime_id = $1 AND status = 'open' AND turn_status = 'queued'
		ORDER BY updated_at, id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, runtimeID, slots)
	if err != nil {
		return nil, fmt.Errorf("testchat: claim select: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []contracts.TaskBundle
	for _, id := range ids {
		b, err := dispatch(ctx, tx, id, runtimeID, now)
		if err != nil {
			return nil, err
		}
		if b != nil {
			out = append(out, *b)
		}
	}
	return out, nil
}

// InFlightOn counts the turns out with runtimeID — what they occupy of the
// runtime's concurrency cap (FR-6.3), alongside the session tasks.
func InFlightOn(ctx context.Context, tx pgx.Tx, runtimeID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM test_chat WHERE runtime_id = $1 AND turn_status IN ('dispatched', 'preparing', 'running')`, runtimeID).Scan(&n)
	return n, err
}

// dispatch marks one queued turn dispatched and builds its bundle. A runtime
// with no `workdir_root` (probe §3 not landed) cannot be given an absolute
// path (§4.1 v0.7.3), so the turn is closed with an error instead of shipping
// a relative one — a test chat is what a person is watching right now, and a
// turn that silently stays queued looks exactly like a hung machine.
func dispatch(ctx context.Context, tx pgx.Tx, id, runtimeID uuid.UUID, now time.Time) (*contracts.TaskBundle, error) {
	r, err := Get(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var (
		agentName, agentRole, roleDesc, instructions string
		toolsJSON, optionsJSON, envJSON              []byte
		args                                         []string
		runtimeKind, model                           string
		budgetPerTask                                *float64
		root                                         *string
	)
	if err := tx.QueryRow(ctx, `
		SELECT a.name, a.role, a.role_description, a.instructions, a.tools, a.budget_per_task,
		       p.runtime_kind, p.model, p.options, p.env, p.args, rt.workdir_root
		FROM test_chat c
		JOIN agent a ON a.id = c.agent_id
		JOIN agent_profile p ON p.id = c.profile_id
		JOIN runtime rt ON rt.id = $2
		WHERE c.id = $1`, id, runtimeID).Scan(
		&agentName, &agentRole, &roleDesc, &instructions, &toolsJSON, &budgetPerTask,
		&runtimeKind, &model, &optionsJSON, &envJSON, &args, &root); err != nil {
		return nil, fmt.Errorf("testchat: bundle rows: %w", err)
	}
	if root == nil || *root == "" {
		if err := closeTurn(ctx, tx, r, Turn{Role: "agent", At: now, Error: errText(noWorkdirRoot)}, nil, now); err != nil {
			return nil, err
		}
		return nil, nil
	}
	var tools []string
	_ = json.Unmarshal(toolsJSON, &tools)
	var options map[string]any
	_ = json.Unmarshal(optionsJSON, &options)
	var env map[string]string
	_ = json.Unmarshal(envJSON, &env)

	// The user turn this attempt answers is the last one (PostTurn appended it
	// and set turn_status = queued in the same statement).
	prompt := ""
	for i := len(r.Turns) - 1; i >= 0; i-- {
		if r.Turns[i].Role == "user" {
			prompt = r.Turns[i].Content
			break
		}
	}
	if r.TurnNo == 1 {
		prompt = FirstTurnPreamble + "\n\n" + prompt
	}

	// Brief: [1] identity and instructions only — no [2] (colab commands: the
	// agent has no token and no MCP server, §4.5), no [4]/[5] (no session, no
	// roster). Same transport rule as a session bundle (harness §1).
	var brief strings.Builder
	fmt.Fprintf(&brief, "[1] Agent Identity\nYou are %s, %s in the Colab workspace. %s\n\nInstructions:\n%s\n\n", agentName, agentRole, roleDesc, instructions)
	brief.WriteString("[8] Instruction precedence: user instruction > agent instructions > runtime defaults.\n")
	transport := contracts.BriefACPMetaSystemPrompt
	adapterPin := contracts.ClaudeAgentACPPin
	if contracts.RuntimeKind(runtimeKind) == contracts.RuntimeHermes {
		transport = contracts.BriefInstructionFile
		adapterPin = ""
	}

	wd := WorkdirPath(*root, r.ID)
	b := &contracts.TaskBundle{
		Task: contracts.BundleTask{
			ID: r.ID.String(), Attempt: r.TurnNo, Kind: "test_chat", TestChatID: r.ID.String(),
			AgentID: r.AgentID.String(), AgentName: agentName, BudgetUSD: budgetPerTask,
		},
		TaskToken: "",
		Profile: contracts.BundleProfile{
			RuntimeKind: contracts.RuntimeKind(runtimeKind), Model: model, Options: options, Env: env, Args: args, Tools: tools, AdapterPin: adapterPin,
		},
		Workdir: contracts.BundleWorkdir{Kind: "dir", Path: wd, Reuse: true},
		Brief:   contracts.BundleBrief{Transport: transport, Text: brief.String()},
		Prompt:  prompt,
		Resume:  bundleResume(r.RuntimeSessionRef, runtimeKind),
		Limits:  contracts.BundleLimits{BudgetUSD: budgetPerTask, StallSeconds: int(contracts.StallTimeout.Seconds())},
	}
	if _, err := tx.Exec(ctx, `
		UPDATE test_chat SET turn_status = 'dispatched', dispatched_at = $2, workdir_path = $3, updated_at = $2
		WHERE id = $1`, r.ID, now, wd); err != nil {
		return nil, fmt.Errorf("testchat: dispatch: %w", err)
	}
	return b, nil
}

// noWorkdirRoot is the turn error when the machine has not reported where its
// working folders live (S-55's refusal, in the user's words).
const noWorkdirRoot = "컴퓨터가 작업 폴더의 기준 위치를 아직 알려 주지 않아 시험 대화를 시작할 수 없습니다 — 컴퓨터를 다시 연결한 뒤 시도해 주세요"

// bundleResume is §4.5 "턴마다 resume 을 이어 한 런타임 세션으로 대화가
// 이어진다": the ref finish stored last turn, if the profile's runtime can
// load it (E8-08 — a ref from another runtime kind is not resumable).
func bundleResume(stored []byte, runtimeKind string) *contracts.RuntimeSessionRef {
	if len(stored) == 0 {
		return nil
	}
	var ref contracts.RuntimeSessionRef
	if err := json.Unmarshal(stored, &ref); err != nil || ref.SessionID == "" {
		return nil
	}
	if string(ref.RuntimeKind) != runtimeKind {
		return nil
	}
	return &ref
}
