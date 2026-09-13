package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/testchat"
)

// ── test chat (FR-1.8.1, openapi /agents/{id}/test-chats · /test-chats/{id}) ──

// CreateTestChat opens a chat (권한: 워크스페이스 멤버). The runtime is fixed
// here (daemon-protocol v0.8 §4.5); none available → 409 in the user's words.
func (s *Server) CreateTestChat(w http.ResponseWriter, r *http.Request, agentId gen.AgentId, params gen.CreateTestChatParams) {
	u, p := s.agentAccess(r, agentId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	wsID, err := s.Agents.WorkspaceOf(r.Context(), agentId)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.CreateTestChatJSONBody
	if len(body) > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		tc, err := s.TestChats.Create(r.Context(), testchat.CreateInput{
			WorkspaceID: wsID, AgentID: agentId, UserID: u.Id,
			ProfileID: optionalUUID(in.ProfileId), RuntimeID: optionalUUID(in.RuntimeId),
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, testChatAPI(tc), nil
	})
}

// testChatAccess is "권한: 그 채팅을 연 사용자" — the chat's own user, and
// nobody else (a workspace member who is not the opener gets 403).
func (s *Server) testChatAccess(r *http.Request, id uuid.UUID) (*testchat.Row, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, p
	}
	tc, err := testchat.Get(r.Context(), s.DB, id)
	if errors.Is(err, testchat.ErrNotFound) {
		return nil, testChatNotFound()
	}
	if err != nil {
		return nil, apperr.As(err)
	}
	if tc.UserID != u.Id {
		// The opener only. A member of the same workspace is told so (403);
		// anyone else learns nothing about whether the chat exists (404, as
		// with agents).
		if m, err := s.Auth.Member(r.Context(), tc.WorkspaceID, u.Id); err == nil && m != nil {
			return nil, apperr.Forbidden("not_chat_owner", "이 시험 대화를 연 사람만 볼 수 있습니다")
		}
		return nil, testChatNotFound()
	}
	return tc, nil
}

func (s *Server) GetTestChat(w http.ResponseWriter, r *http.Request, testChatId gen.TestChatId) {
	tc, p := s.testChatAccess(r, testChatId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	writeJSON(w, http.StatusOK, testChatAPI(tc))
}

// PostTestChatTurn queues one user turn (202 with the turn; the agent's answer
// arrives over SSE test_chat.delta / test_chat.turn). 409 while a turn is in
// flight, 410 once closed.
func (s *Server) PostTestChatTurn(w http.ResponseWriter, r *http.Request, testChatId gen.TestChatId, params gen.PostTestChatTurnParams) {
	tc, p := s.testChatAccess(r, testChatId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.PostTestChatTurnJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if in.Content == "" {
		writeProblem(w, apperr.Validation(apperr.Field("content", "required", "보낼 메시지를 적어 주세요")))
		return
	}
	s.idempotent(r.Context(), w, "user:"+tc.UserID.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		turn, err := s.TestChats.PostTurn(r.Context(), tc.ID, in.Content)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusAccepted, testChatTurnAPI(*turn), nil
	})
}

// CloseTestChat ends the chat (idempotent — already closed answers 200 too).
// The daemon gets `cancel` for a turn in flight and `gc` for the directory
// (§4.5 "닫기·취소").
func (s *Server) CloseTestChat(w http.ResponseWriter, r *http.Request, testChatId gen.TestChatId) {
	tc, p := s.testChatAccess(r, testChatId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.TestChats.Close(r.Context(), tc.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, testChatAPI(out))
}

// testChatNotFound is the 404 for a chat the caller may not see. It is
// composed here rather than through apperr.NotFound("test_chat") because
// apperr.NotFoundNouns is mirrored item for item by
// web/lib/mock/server-wording.test.ts (b), and T-S12 may not touch web/ —
// adding the noun there turned the web CI job red on this PR. When the web
// mirror gains `test_chat: 시험 대화`, move this back into the table.
func testChatNotFound() *Problem {
	return apperr.New(http.StatusNotFound, "not_found", "시험 대화를 찾을 수 없습니다")
}

func optionalUUID(n nullable.Nullable[openapi_types.UUID]) *uuid.UUID {
	if !n.IsSpecified() || n.IsNull() {
		return nil
	}
	v := n.MustGet()
	return &v
}

// testChatAPI renders a row as openapi TestChat.
func testChatAPI(tc *testchat.Row) gen.TestChat {
	out := gen.TestChat{
		Id: tc.ID, WorkspaceId: tc.WorkspaceID, AgentId: tc.AgentID, ProfileId: tc.ProfileID, UserId: tc.UserID,
		Status:      gen.TestChatStatus(tc.Status),
		InputTokens: tc.InputTokens, OutputTokens: tc.OutputTokens, CostUsd: float32(tc.CostUSD),
		CreatedAt: tc.CreatedAt.UTC(), UpdatedAt: tc.UpdatedAt.UTC(),
		ClosedAt: nullableTime(tc.ClosedAt),
		Turns:    make([]gen.TestChatTurn, 0, len(tc.Turns)),
	}
	est := tc.Estimated
	out.Estimated = &est
	if tc.RuntimeID != nil {
		out.RuntimeId = nullable.NewNullableWithValue(*tc.RuntimeID)
	} else {
		out.RuntimeId = nullable.NewNullNullable[openapi_types.UUID]()
	}
	if tc.Transport != nil {
		out.Transport = nullable.NewNullableWithValue(gen.TestChatTransport(*tc.Transport))
	} else {
		out.Transport = nullable.NewNullNullable[gen.TestChatTransport]()
	}
	for _, t := range tc.Turns {
		out.Turns = append(out.Turns, testChatTurnAPI(t))
	}
	return out
}

// testChatTurnAPI renders one turn as openapi TestChatTurn — also the `turn`
// of SSE test_chat.turn, so the stream and the REST read agree byte for byte.
func testChatTurnAPI(t testchat.Turn) gen.TestChatTurn {
	out := gen.TestChatTurn{Role: gen.TestChatTurnRole(t.Role), Content: t.Content, At: t.At.UTC()}
	if t.Error != nil {
		out.Error = nullable.NewNullableWithValue(*t.Error)
	}
	if t.Usage != nil {
		in, o := int(t.Usage.InputTokens), int(t.Usage.OutputTokens)
		out.Usage = &struct {
			InputTokens  *int `json:"input_tokens,omitempty"`
			OutputTokens *int `json:"output_tokens,omitempty"`
		}{InputTokens: &in, OutputTokens: &o}
	}
	return out
}
