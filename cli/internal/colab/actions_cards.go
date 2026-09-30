// Card commands — contracts/colab-cli.md v0.9.10 (PRD FR-3.8 · openapi
// v0.3.10): card delegate · report · accept · revise · get · list, and the
// retired `lane delegate`. The CLI (cmd/colab) and the MCP server call these,
// so a tool's arguments and result are exactly the command's.
package colab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// CardRequiredSentence is colab-cli v0.9.10's refusal of the old
// `lane delegate` — never sent to the server.
const CardRequiredSentence = "위임은 카드로 합니다: colab card delegate --file <card.json> (목표·완료 기준과 확인 방법·하지 않을 것)"

// LaneDelegateRetired is `colab lane delegate …` since v0.9.10: exit 3
// card_required, nothing sent.
func LaneDelegateRetired() error {
	return &client.Error{Exit: client.ExitRefused, Code: "card_required", Title: "delegation needs a card", Detail: CardRequiredSentence}
}

// NotParticipantHint is the alternative route E15-02 requires the CLI to name
// when the delegate target is outside the session (FR-1.5: agents cannot
// create participants).
const NotParticipantHint = "ask the Director to add them as a participant with `colab hitl ask` " +
	"(agents cannot add participants — FR-1.5); then retry `colab card delegate`"

// NotCardTaskSentence is `card report` outside a card task (before the
// server, from getCliContext).
const NotCardTaskSentence = "이 턴은 카드로 받은 일이 아닙니다 — 결과 카드는 카드로 받은 턴에서만 냅니다"

// ───────────────────────────── card delegate ─────────────────────────────

// CardDelegateArgs — `colab card delegate --file <card.json> [--depends-on]
// [--profile]` / colab_card_delegate. Card is the card file's object
// (openapi TaskCardInput with `agent` — a participant name — for agent_id);
// the MCP tool sends the same fields flat, the CLI reads them from File.
type CardDelegateArgs struct {
	File      string         `json:"file,omitempty"`
	Card      map[string]any `json:"-"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Profile   string         `json:"profile,omitempty"`
	Session   string         `json:"session,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// CardDelegateResult — colab-cli v0.9.10 output {card_label, card_id,
// lane_id, task_id} + the approval-pending notice.
type CardDelegateResult struct {
	CardLabel string          `json:"card_label"`
	CardID    string          `json:"card_id"`
	LaneID    string          `json:"lane_id"`
	TaskID    string          `json:"task_id,omitempty"`
	AgentID   string          `json:"agent_id"`
	AgentName string          `json:"agent_name"`
	MessageID string          `json:"message_id,omitempty"`
	Card      json.RawMessage `json:"card,omitempty"`
	Notice    string          `json:"notice,omitempty"`
}

// readJSONFile is a --file argument: a JSON object.
func readJSONFile(path, what string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, client.Usage("%s: %v", what, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, client.Usage("%s: not a JSON object: %v", what, err)
	}
	return m, nil
}

// CardDelegate — POST /rooms/{R}/lanes with {card, depends_on, profile}.
func CardDelegate(ctx context.Context, c *client.Client, a CardDelegateArgs) (*CardDelegateResult, error) {
	card := a.Card
	if a.File != "" {
		m, err := readJSONFile(a.File, "--file")
		if err != nil {
			return nil, err
		}
		card = m
	}
	if card == nil {
		return nil, client.Usage("--file <card.json> is required")
	}
	if err := c.Allow(ctx, client.CmdCardDelegate); err != nil {
		return nil, err
	}
	sid, err := c.RoomID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	for k, v := range card {
		body[k] = v
	}
	name, _ := body["agent"].(string)
	delete(body, "agent")
	var p client.Participant
	if id, ok := body["agent_id"].(string); ok && id != "" && name == "" {
		name = id
	}
	if strings.TrimSpace(name) != "" {
		var ok bool
		if p, ok = cc.AgentByName(name); !ok {
			return nil, &client.Error{
				Exit: client.ExitRefused, Code: "not_participant",
				Title: "@" + strings.TrimPrefix(name, "@") + " is not a room participant",
				Detail: "cannot delegate to a non-participant. participants: " +
					strings.Join(cc.ParticipantNames(), ", ") + ". " + NotParticipantHint,
			}
		}
		body["agent_id"] = p.AgentID
	}
	// A missing agent goes to the server as is: its card_invalid errors[]
	// names every missing field at once (card.agent_id: required).
	req := client.CardDelegateCreate{Card: body, DependsOn: splitList(a.DependsOn)}
	if a.Profile != "" {
		prof := a.Profile
		req.Profile = &prof
	}
	res, err := c.DelegateCard(ctx, sid, req, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	out := &CardDelegateResult{AgentID: p.AgentID, AgentName: p.Name, Card: res.Card}
	out.LaneID = rawField(res.Lane, "id")
	out.TaskID = rawField(res.Task, "id")
	out.CardID = rawField(res.Card, "id")
	out.CardLabel = rawField(res.Card, "label")
	if rawField(res.Task, "queued_reason") == client.QueuedApprovalPending {
		out.Notice = QuietNotice(p.Name)
	}
	if res.Message != nil {
		out.MessageID = res.Message.ID
	}
	return out, nil
}

// ───────────────────────────── card report ─────────────────────────────

// CardReportArgs — `colab card report --file <result.json>` /
// colab_card_report (openapi CardResultInput, flat in the tool).
type CardReportArgs struct {
	File   string         `json:"file,omitempty"`
	Result map[string]any `json:"-"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// CardReportResult — submitCardResult 200: the card, and the downgrade
// notice (harness v0.9.16) when the server saved a met as partial.
type CardReportResult struct {
	Card   json.RawMessage `json:"card"`
	Notice string          `json:"notice,omitempty"`
}

// CardReport — POST /cards/{C}/result, C = getCliContext.card_id.
func CardReport(ctx context.Context, c *client.Client, a CardReportArgs) (*CardReportResult, error) {
	res := a.Result
	if a.File != "" {
		m, err := readJSONFile(a.File, "--file")
		if err != nil {
			return nil, err
		}
		res = m
	}
	if res == nil {
		return nil, client.Usage("--file <result.json> is required")
	}
	if err := c.Allow(ctx, client.CmdCardReport); err != nil {
		return nil, err
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return nil, err
	}
	if cc.CardID == nil || *cc.CardID == "" {
		return nil, &client.Error{Exit: client.ExitRefused, Code: "not_card_task", Title: "not a card task", Detail: NotCardTaskSentence}
	}
	raw, err := c.CardCall(ctx, http.MethodPost, "/cards/"+url.PathEscape(*cc.CardID)+"/result", nil, res, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	var out CardReportResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &client.Error{Exit: client.ExitUnreachable, Code: "bad_response", Title: "unparseable server response", Detail: err.Error()}
	}
	return &out, nil
}

// ───────────────────────────── accept · revise · get · list ─────────────

var labelRe = regexp.MustCompile(`^[Cc]-([1-9][0-9]*)$`)

// resolveCard turns `C-n` (the turn's mission board) or an id into an id.
func resolveCard(ctx context.Context, c *client.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", client.Usage("card (C-n or id) is required")
	}
	m := labelRe.FindStringSubmatch(ref)
	if m == nil {
		return ref, nil
	}
	n, _ := strconv.Atoi(m[1])
	sid, err := c.RoomID(ctx, "")
	if err != nil {
		return "", err
	}
	_, list, err := c.ListRoomCards(ctx, sid, c.WorkID())
	if err != nil {
		return "", err
	}
	for _, k := range list {
		if k.Number == n {
			return k.ID, nil
		}
	}
	return "", &client.Error{Exit: client.ExitRefused, Code: "not_found", Title: "card not found",
		Detail: fmt.Sprintf("이 미션에 %s 카드가 없습니다 — colab card list 로 번호를 확인하세요", strings.ToUpper(ref))}
}

// CardJudgeArgs — `colab card accept <C-n|id>` · `colab card revise <C-n|id>
// --reason <t> [--file <patch.json>]` / colab_card_accept · colab_card_revise.
type CardJudgeArgs struct {
	Card   string         `json:"card"`
	Reason string         `json:"reason,omitempty"`
	File   string         `json:"file,omitempty"`
	Patch  map[string]any `json:"patch,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// CardAccept — POST /cards/{C}/accept.
func CardAccept(ctx context.Context, c *client.Client, a CardJudgeArgs) (json.RawMessage, error) {
	if err := c.Allow(ctx, client.CmdCardAccept); err != nil {
		return nil, err
	}
	id, err := resolveCard(ctx, c, a.Card)
	if err != nil {
		return nil, err
	}
	return c.CardCall(ctx, http.MethodPost, "/cards/"+url.PathEscape(id)+"/accept", nil, map[string]any{}, a.IdempotencyKey)
}

// CardRevise — POST /cards/{C}/revise {reason, card?}.
func CardRevise(ctx context.Context, c *client.Client, a CardJudgeArgs) (json.RawMessage, error) {
	if strings.TrimSpace(a.Reason) == "" {
		return nil, client.Usage("--reason is required")
	}
	patch := a.Patch
	if a.File != "" {
		m, err := readJSONFile(a.File, "--file")
		if err != nil {
			return nil, err
		}
		patch = m
	}
	if err := c.Allow(ctx, client.CmdCardRevise); err != nil {
		return nil, err
	}
	id, err := resolveCard(ctx, c, a.Card)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"reason": a.Reason}
	if patch != nil {
		body["card"] = patch
	}
	return c.CardCall(ctx, http.MethodPost, "/cards/"+url.PathEscape(id)+"/revise", nil, body, a.IdempotencyKey)
}

// CardGetArgs — `colab card get <C-n|id>`.
type CardGetArgs struct {
	Card string `json:"card"`
}

// CardGet — GET /cards/{C} (versions included).
func CardGet(ctx context.Context, c *client.Client, a CardGetArgs) (json.RawMessage, error) {
	if err := c.Allow(ctx, client.CmdCardGet); err != nil {
		return nil, err
	}
	id, err := resolveCard(ctx, c, a.Card)
	if err != nil {
		return nil, err
	}
	return c.CardCall(ctx, http.MethodGet, "/cards/"+url.PathEscape(id), nil, nil, "")
}

// CardListArgs — `colab card list`.
type CardListArgs struct {
	Session string `json:"session,omitempty"`
}

// CardList — GET /rooms/{R}/cards?work_id=<COLAB_WORK_ID|none>.
func CardList(ctx context.Context, c *client.Client, a CardListArgs) (json.RawMessage, error) {
	if err := c.Allow(ctx, client.CmdCardList); err != nil {
		return nil, err
	}
	sid, err := c.RoomID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	raw, _, err := c.ListRoomCards(ctx, sid, c.WorkID())
	return raw, err
}
