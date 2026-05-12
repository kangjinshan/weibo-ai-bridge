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

func TestAllowAllFromContext(t *testing.T) {
	if AllowAllFromContext(context.Background()) {
		t.Fatal("allow all should be disabled by default")
	}

	ctx := WithAllowAll(context.Background(), true)
	if !AllowAllFromContext(ctx) {
		t.Fatal("allow all should be enabled")
	}
}
