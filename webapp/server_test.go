package webapp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quiz-cli/quiz"
)

func TestServerFlowStateAnswerReset(t *testing.T) {
	qs := []quiz.Question{
		{
			Domain:  1,
			Prompt:  "Sky color?",
			Options: map[string]string{"A": "Green", "B": "Blue"},
			Answer:  "B",
		},
	}
	s := &Server{
		session:   quiz.NewSession(qs),
		questions: qs,
	}

	// Initial state
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	s.handleState(rr, req)
	var state stateResponse
	decodeBody(t, rr.Body.Bytes(), &state)
	if state.Finished {
		t.Fatalf("expected quiz not finished")
	}
	if state.Progress.Completed != 0 || state.Progress.Total != 1 {
		t.Fatalf("unexpected progress: %+v", state.Progress)
	}

	// Wrong answer should not finish quiz
	answerRR := httptest.NewRecorder()
	wrongBody := bytes.NewBufferString(`{"answer":"A"}`)
	answerReq := httptest.NewRequest(http.MethodPost, "/api/answer", wrongBody)
	s.handleAnswer(answerRR, answerReq)
	var wrongResp answerResponse
	decodeBody(t, answerRR.Body.Bytes(), &wrongResp)
	if wrongResp.Finished {
		t.Fatalf("quiz should not finish on wrong answer")
	}
	if wrongResp.Progress.Completed != 0 {
		t.Fatalf("wrong answer should not mark completed")
	}

	// Correct answer finishes quiz
	answerRR = httptest.NewRecorder()
	correctBody := bytes.NewBufferString(`{"answer":"B"}`)
	answerReq = httptest.NewRequest(http.MethodPost, "/api/answer", correctBody)
	s.handleAnswer(answerRR, answerReq)
	var correctResp answerResponse
	decodeBody(t, answerRR.Body.Bytes(), &correctResp)
	if !correctResp.Finished {
		t.Fatalf("quiz should finish after answering correctly")
	}
	if correctResp.Progress.Completed != 1 {
		t.Fatalf("expected completed=1, got %d", correctResp.Progress.Completed)
	}

	// Summary reflects first-attempt grading (still 0 because first was wrong)
	summaryRR := httptest.NewRecorder()
	s.handleSummary(summaryRR, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	var summary summaryPayload
	decodeBody(t, summaryRR.Body.Bytes(), &summary)
	if summary.Score != 0 || summary.Answered != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	// Reset to start over
	resetRR := httptest.NewRecorder()
	s.handleReset(resetRR, httptest.NewRequest(http.MethodPost, "/api/reset", nil))
	if resetRR.Code != http.StatusOK {
		t.Fatalf("reset returned status %d", resetRR.Code)
	}

	// After reset, state should not be finished
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/state", nil)
	s.handleState(rr, req)
	decodeBody(t, rr.Body.Bytes(), &state)
	if state.Finished || state.Progress.Completed != 0 || state.Progress.Attempted != 0 {
		t.Fatalf("state after reset invalid: %+v", state.Progress)
	}
}

func TestJumpSearchMovesQuestionToFront(t *testing.T) {
	qs := []quiz.Question{
		{Domain: 1, Prompt: "Sky color?", Options: map[string]string{"A": "Blue", "B": "Red"}, Answer: "A"},
		{Domain: 2, Prompt: "Grass color?", Options: map[string]string{"A": "Blue", "B": "Green"}, Answer: "B"},
	}
	s := &Server{
		session:   quiz.NewSession(qs),
		questions: qs,
	}

	body := bytes.NewBufferString(`{"term":"grass"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/jump", body)
	s.handleJump(rr, req)

	var resp jumpResponse
	decodeBody(t, rr.Body.Bytes(), &resp)
	if !resp.Found {
		t.Fatalf("expected to find a match")
	}
	if resp.Index != 2 || resp.Domain != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	idx, _, ok := s.session.Current()
	if !ok {
		t.Fatalf("session should still have questions")
	}
	if idx != 1 {
		t.Fatalf("expected question 2 to be brought to front, got idx %d", idx)
	}
}

func decodeBody(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode body: %v\nbody: %s", err, string(data))
	}
}
