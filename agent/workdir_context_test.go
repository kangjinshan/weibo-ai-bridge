package agent

import (
	"context"
	"testing"
)

func TestWithWorkDir_NilContext(t *testing.T) {
	if got := WithWorkDir(nil, "/tmp/work"); got != nil {
		t.Fatalf("expected nil context, got %v", got)
	}
}

func TestWorkDirFromContext(t *testing.T) {
	ctx := WithWorkDir(context.Background(), "  /tmp/project-a  ")
	if got := WorkDirFromContext(ctx); got != "/tmp/project-a" {
		t.Fatalf("unexpected work dir: %q", got)
	}
}
