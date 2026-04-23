package agent

import "testing"

func TestWrapUserPrompt_PreservesInputVerbatim(t *testing.T) {
	input := "排查一下为什么服务没起来，并直接修复"

	got := wrapUserPrompt(input)

	if got != input {
		t.Fatalf("expected input to pass through unchanged, got %q", got)
	}
}

func TestWrapUserPrompt_PreservesWhitespaceAndFormatting(t *testing.T) {
	input := "第一行\n\n- 第二行\n"

	got := wrapUserPrompt(input)

	if got != input {
		t.Fatalf("expected formatting to be preserved, got %q", got)
	}
}
