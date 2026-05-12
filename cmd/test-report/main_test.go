package main

import (
	"strings"
	"testing"
)

func TestParseGoTestJSONExtractsPackageCoverageAndFailures(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"Time":"2026-05-12T10:00:00Z","Action":"output","Package":"example.com/app/router","Output":"ok  \texample.com/app/router\t0.123s\tcoverage: 72.5% of statements\n"}`,
		`{"Time":"2026-05-12T10:00:00Z","Action":"output","Package":"example.com/app/router","Output":"coverage: 72.5% of statements\n"}`,
		`{"Time":"2026-05-12T10:00:01Z","Action":"fail","Package":"example.com/app/router","Test":"TestRouterRejectsBadInput","Elapsed":0.02}`,
		`{"Time":"2026-05-12T10:00:02Z","Action":"output","Package":"example.com/app/agent","Output":"FAIL\texample.com/app/agent\t0.456s\n"}`,
		`{"Time":"2026-05-12T10:00:02Z","Action":"fail","Package":"example.com/app/agent","Elapsed":0.456}`,
		"",
	}, "\n"))

	result, err := parseGoTestJSON(input)
	if err != nil {
		t.Fatalf("parseGoTestJSON returned error: %v", err)
	}

	if len(result.PackageCoverages) != 1 {
		t.Fatalf("expected one package coverage, got %#v", result.PackageCoverages)
	}
	if got := result.PackageCoverages[0]; got.Package != "example.com/app/router" || got.Coverage != 72.5 {
		t.Fatalf("unexpected package coverage: %#v", got)
	}

	if len(result.Failures) != 2 {
		t.Fatalf("expected two failures, got %#v", result.Failures)
	}
	if result.Failures[0].Package != "example.com/app/router" || result.Failures[0].Test != "TestRouterRejectsBadInput" {
		t.Fatalf("unexpected test failure: %#v", result.Failures[0])
	}
	if result.Failures[1].Package != "example.com/app/agent" || result.Failures[1].Test != "" {
		t.Fatalf("unexpected package failure: %#v", result.Failures[1])
	}
}

func TestParseCoverFuncExtractsLowCoverageAndTotal(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`github.com/example/app/router/stream_sender.go:61:	PushPartialSnapshot		0.0%`,
		`github.com/example/app/session/session.go:371:	ContextBool			42.9%`,
		`github.com/example/app/config/config.go:259:	Validate			100.0%`,
		`total:						(statements)			63.7%`,
		"",
	}, "\n"))

	result, err := parseCoverFunc(input, 50)
	if err != nil {
		t.Fatalf("parseCoverFunc returned error: %v", err)
	}

	if result.TotalCoverage != 63.7 {
		t.Fatalf("unexpected total coverage: %.1f", result.TotalCoverage)
	}
	if len(result.LowCoverageFunctions) != 2 {
		t.Fatalf("expected two low coverage functions, got %#v", result.LowCoverageFunctions)
	}
	if result.LowCoverageFunctions[0].Name != "PushPartialSnapshot" || result.LowCoverageFunctions[0].Coverage != 0 {
		t.Fatalf("unexpected first low coverage function: %#v", result.LowCoverageFunctions[0])
	}
}

func TestRenderReportsIncludeCoverageFailuresAndArtifacts(t *testing.T) {
	report := testReport{
		GeneratedAt:  "2026-05-12T10:00:00Z",
		CoverageFile: "reports/coverage.out",
		JSONLogFile:  "reports/go-test.jsonl",
		TestExitCode: 1,
		PackageCoverages: []packageCoverage{
			{Package: "example.com/app/router", Coverage: 72.5},
		},
		LowCoverageFunctions: []functionCoverage{
			{Location: "router/stream_sender.go:61:", Name: "PushPartialSnapshot", Coverage: 0},
		},
		Failures: []testFailure{
			{Package: "example.com/app/router", Test: "TestRouterRejectsBadInput"},
		},
		TotalCoverage: 63.7,
	}

	markdown := renderMarkdown(report)
	for _, want := range []string{
		"# Test Report",
		"`reports/coverage.out`",
		"example.com/app/router",
		"PushPartialSnapshot",
		"TestRouterRejectsBadInput",
		"63.7%",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown report missing %q:\n%s", want, markdown)
		}
	}

	text := renderText(report)
	for _, want := range []string{"Test Report", "Coverage file: reports/coverage.out", "Failures:", "Low coverage functions:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text report missing %q:\n%s", want, text)
		}
	}
}
