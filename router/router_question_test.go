package router

import (
	"strings"
	"testing"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
)

func TestFormatQuestionPrompt(t *testing.T) {
	q := agent.UserQuestion{
		Question: "选哪个?",
		Options: []agent.UserQuestionOption{
			{Label: "A", Description: "第一项"},
			{Label: "B"},
		},
	}

	t.Run("single question no index", func(t *testing.T) {
		got := formatQuestionPrompt(q, 0, 1)
		if strings.Contains(got, "/") && strings.Contains(got, "（1/1）") {
			t.Errorf("single question should not show index: %q", got)
		}
		if !strings.Contains(got, "1. A — 第一项") || !strings.Contains(got, "2. B") {
			t.Errorf("options not rendered: %q", got)
		}
	})

	t.Run("multi question shows index", func(t *testing.T) {
		got := formatQuestionPrompt(q, 1, 3)
		if !strings.Contains(got, "（2/3）") {
			t.Errorf("expected index 2/3: %q", got)
		}
	})

	t.Run("multiSelect hint", func(t *testing.T) {
		mq := q
		mq.MultiSelect = true
		got := formatQuestionPrompt(mq, 0, 1)
		if !strings.Contains(got, "可多选") {
			t.Errorf("expected multi-select hint: %q", got)
		}
	})
}

func TestResolveQuestionAnswer(t *testing.T) {
	q := agent.UserQuestion{
		Options: []agent.UserQuestionOption{
			{Label: "Red"}, {Label: "Green"}, {Label: "Blue"},
		},
	}

	t.Run("single numeric maps to label", func(t *testing.T) {
		if got := resolveQuestionAnswer(q, "2"); got != "Green" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("out of range falls back to raw", func(t *testing.T) {
		if got := resolveQuestionAnswer(q, "9"); got != "9" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("non-numeric falls back to raw", func(t *testing.T) {
		if got := resolveQuestionAnswer(q, "purple"); got != "purple" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("multiselect comma separated", func(t *testing.T) {
		mq := q
		mq.MultiSelect = true
		if got := resolveQuestionAnswer(mq, "1，3"); got != "Red, Blue" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("multiselect partial invalid falls back", func(t *testing.T) {
		mq := q
		mq.MultiSelect = true
		if got := resolveQuestionAnswer(mq, "1, foo"); got != "1, foo" {
			t.Errorf("got %q", got)
		}
	})
}

func TestInteractiveStateQuestionLifecycle(t *testing.T) {
	st := &interactiveSessionState{}
	questions := []agent.UserQuestion{
		{Question: "q1", Options: []agent.UserQuestionOption{{Label: "a"}}},
		{Question: "q2", Options: []agent.UserQuestionOption{{Label: "b"}}},
	}

	st.BeginQuestions(questions)
	if !st.HasPendingQuestions() || !st.AwaitingApproval() {
		t.Fatal("expected pending questions and awaiting approval")
	}

	cur, idx, total, ok := st.CurrentQuestion()
	if !ok || cur.Question != "q1" || idx != 0 || total != 2 {
		t.Fatalf("unexpected current: %+v idx=%d total=%d ok=%v", cur, idx, total, ok)
	}

	next, nextIdx, total, hasNext, collected := st.RecordAnswerAndAdvance("answer1")
	if !hasNext || next.Question != "q2" || nextIdx != 1 || collected != nil {
		t.Fatalf("expected advance to q2: next=%+v idx=%d hasNext=%v", next, nextIdx, hasNext)
	}

	_, _, _, hasNext, collected = st.RecordAnswerAndAdvance("answer2")
	if hasNext {
		t.Fatal("expected no more questions")
	}
	if collected[0] != "answer1" || collected[1] != "answer2" {
		t.Fatalf("collected = %+v", collected)
	}

	st.ResetQuestions()
	if st.HasPendingQuestions() || st.AwaitingApproval() {
		t.Fatal("expected questions cleared after reset")
	}
}

func TestHandleQuestionReply_AdvanceAndAnswer(t *testing.T) {
	r := &Router{}
	mockSession := NewMockInteractiveSession()
	st := &interactiveSessionState{session: mockSession}
	st.BeginQuestions([]agent.UserQuestion{
		{Question: "q1", Options: []agent.UserQuestionOption{{Label: "Red"}, {Label: "Blue"}}},
		{Question: "q2", Options: []agent.UserQuestionOption{{Label: "Cat"}, {Label: "Dog"}}},
	})

	var emitted []agent.Event
	emit := func(e agent.Event) { emitted = append(emitted, e) }

	// First reply: choose option 2 of q1 → advance to q2
	if err := r.handleQuestionReply(t.Context(), &Message{Content: "2"}, nil, "", "", nil, st, emit); err != nil {
		t.Fatalf("first reply: %v", err)
	}
	if len(mockSession.answeredInputs) != 0 {
		t.Fatal("should not answer until all questions done")
	}
	if !st.HasPendingQuestions() {
		t.Fatal("should still have pending questions")
	}
	// Expect a confirmation + next prompt
	joined := ""
	for _, e := range emitted {
		joined += e.Content + "\n"
	}
	if !strings.Contains(joined, "已选择：Blue") || !strings.Contains(joined, "q2") {
		t.Fatalf("unexpected emits after first reply: %q", joined)
	}

	// Second reply: choose option 1 of q2 → finalize
	emitted = nil
	// Preload the turn that resumes after answers are sent back to Claude.
	mockSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "final answer"}
	mockSession.events <- agent.Event{Type: agent.EventTypeDone}
	if err := r.handleQuestionReply(t.Context(), &Message{Content: "1"}, nil, "", "", nil, st, emit); err != nil {
		t.Fatalf("second reply: %v", err)
	}
	if len(mockSession.answeredInputs) != 1 {
		t.Fatalf("expected one answer call, got %d", len(mockSession.answeredInputs))
	}
	answers := mockSession.answeredInputs[0]
	if answers[0] != "Blue" || answers[1] != "Cat" {
		t.Fatalf("answers = %+v", answers)
	}
	if st.HasPendingQuestions() {
		t.Fatal("questions should be cleared after finalize")
	}
}
