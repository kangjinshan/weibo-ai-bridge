package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type packageCoverage struct {
	Package  string
	Coverage float64
}

type functionCoverage struct {
	Location string
	Name     string
	Coverage float64
}

type testFailure struct {
	Package string
	Test    string
}

type goTestResult struct {
	PackageCoverages []packageCoverage
	Failures         []testFailure
}

type coverFuncResult struct {
	LowCoverageFunctions []functionCoverage
	TotalCoverage        float64
}

type testReport struct {
	GeneratedAt          string
	CoverageFile         string
	JSONLogFile          string
	TestExitCode         int
	TestError            string
	PackageCoverages     []packageCoverage
	LowCoverageFunctions []functionCoverage
	Failures             []testFailure
	TotalCoverage        float64
}

type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

var packageCoveragePattern = regexp.MustCompile(`coverage:\s+([0-9]+(?:\.[0-9]+)?)%`)

func main() {
	coverageFile := flag.String("coverprofile", "reports/coverage.out", "coverage profile path")
	markdownFile := flag.String("markdown", "reports/test-report.md", "markdown report path")
	textFile := flag.String("text", "reports/test-report.txt", "text report path")
	jsonLogFile := flag.String("json-log", "reports/go-test.jsonl", "go test -json log path")
	threshold := flag.Float64("threshold", 50, "low coverage threshold")
	flag.Parse()

	exitCode, err := run(*coverageFile, *markdownFile, *textFile, *jsonLogFile, *threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test report failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

func run(coverageFile, markdownFile, textFile, jsonLogFile string, threshold float64) (int, error) {
	for _, path := range []string{coverageFile, markdownFile, textFile, jsonLogFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return 1, err
		}
	}

	testOutput, exitCode, testErr := runGoTests(coverageFile)
	if err := os.WriteFile(jsonLogFile, testOutput, 0o644); err != nil {
		return 1, err
	}

	testResult, parseErr := parseGoTestJSON(bytes.NewReader(testOutput))
	if parseErr != nil {
		return 1, parseErr
	}

	coverResult := coverFuncResult{}
	if _, err := os.Stat(coverageFile); err == nil {
		coverOutput, err := exec.Command("go", "tool", "cover", "-func="+coverageFile).Output()
		if err == nil {
			coverResult, err = parseCoverFunc(bytes.NewReader(coverOutput), threshold)
			if err != nil {
				return 1, err
			}
		}
	}

	report := testReport{
		GeneratedAt:          time.Now().Format(time.RFC3339),
		CoverageFile:         coverageFile,
		JSONLogFile:          jsonLogFile,
		TestExitCode:         exitCode,
		PackageCoverages:     testResult.PackageCoverages,
		LowCoverageFunctions: coverResult.LowCoverageFunctions,
		Failures:             testResult.Failures,
		TotalCoverage:        coverResult.TotalCoverage,
	}
	if testErr != nil {
		report.TestError = testErr.Error()
	}

	if err := os.WriteFile(markdownFile, []byte(renderMarkdown(report)), 0o644); err != nil {
		return 1, err
	}
	if err := os.WriteFile(textFile, []byte(renderText(report)), 0o644); err != nil {
		return 1, err
	}

	fmt.Printf("Markdown report: %s\n", markdownFile)
	fmt.Printf("Text report: %s\n", textFile)
	fmt.Printf("Coverage profile: %s\n", coverageFile)
	return exitCode, nil
}

func runGoTests(coverageFile string) ([]byte, int, error) {
	cmd := exec.Command("go", "test", "-json", "-coverprofile="+coverageFile, "./...")
	output, err := cmd.CombinedOutput()
	return output, commandExitCode(err), err
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func parseGoTestJSON(r io.Reader) (goTestResult, error) {
	result := goTestResult{}
	scanner := bufio.NewScanner(r)
	seenPackageFailures := map[string]struct{}{}
	coverageByPackage := map[string]float64{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event goTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		if event.Output != "" {
			if coverage, ok := parsePackageCoverage(event.Output); ok && event.Package != "" {
				coverageByPackage[event.Package] = coverage
			}
		}

		if event.Action != "fail" || event.Package == "" {
			continue
		}
		if event.Test != "" {
			result.Failures = append(result.Failures, testFailure{Package: event.Package, Test: event.Test})
			continue
		}
		if _, ok := seenPackageFailures[event.Package]; ok {
			continue
		}
		seenPackageFailures[event.Package] = struct{}{}
		result.Failures = append(result.Failures, testFailure{Package: event.Package})
	}

	for pkg, coverage := range coverageByPackage {
		result.PackageCoverages = append(result.PackageCoverages, packageCoverage{
			Package:  pkg,
			Coverage: coverage,
		})
	}
	sort.Slice(result.PackageCoverages, func(i, j int) bool {
		return result.PackageCoverages[i].Package < result.PackageCoverages[j].Package
	})

	return result, scanner.Err()
}

func parsePackageCoverage(output string) (float64, bool) {
	match := packageCoveragePattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return 0, false
	}
	coverage, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	return coverage, true
}

func parseCoverFunc(r io.Reader, threshold float64) (coverFuncResult, error) {
	result := coverFuncResult{}
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		coverage, err := parsePercent(fields[len(fields)-1])
		if err != nil {
			continue
		}
		if strings.HasPrefix(fields[0], "total:") {
			result.TotalCoverage = coverage
			continue
		}
		if coverage >= threshold {
			continue
		}

		result.LowCoverageFunctions = append(result.LowCoverageFunctions, functionCoverage{
			Location: fields[0],
			Name:     fields[1],
			Coverage: coverage,
		})
	}

	sort.Slice(result.LowCoverageFunctions, func(i, j int) bool {
		left := result.LowCoverageFunctions[i]
		right := result.LowCoverageFunctions[j]
		if left.Coverage == right.Coverage {
			return left.Name < right.Name
		}
		return left.Coverage < right.Coverage
	})

	return result, scanner.Err()
}

func parsePercent(raw string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
}

func renderMarkdown(report testReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Test Report\n\n")
	fmt.Fprintf(&b, "- Generated at: `%s`\n", report.GeneratedAt)
	fmt.Fprintf(&b, "- Test exit code: `%d`\n", report.TestExitCode)
	fmt.Fprintf(&b, "- Coverage file: `%s`\n", report.CoverageFile)
	fmt.Fprintf(&b, "- Go test JSON log: `%s`\n", report.JSONLogFile)
	if report.TotalCoverage > 0 {
		fmt.Fprintf(&b, "- Total coverage: `%.1f%%`\n", report.TotalCoverage)
	}
	if report.TestError != "" {
		fmt.Fprintf(&b, "- Test command error: `%s`\n", report.TestError)
	}

	fmt.Fprintf(&b, "\n## Package Coverage\n\n")
	if len(report.PackageCoverages) == 0 {
		fmt.Fprintf(&b, "No package coverage was captured.\n")
	} else {
		fmt.Fprintf(&b, "| Package | Coverage |\n| --- | ---: |\n")
		for _, item := range report.PackageCoverages {
			fmt.Fprintf(&b, "| `%s` | %.1f%% |\n", item.Package, item.Coverage)
		}
	}

	fmt.Fprintf(&b, "\n## Low Coverage Functions\n\n")
	if len(report.LowCoverageFunctions) == 0 {
		fmt.Fprintf(&b, "No functions fell below the configured threshold.\n")
	} else {
		fmt.Fprintf(&b, "| Location | Function | Coverage |\n| --- | --- | ---: |\n")
		for _, item := range report.LowCoverageFunctions {
			fmt.Fprintf(&b, "| `%s` | `%s` | %.1f%% |\n", item.Location, item.Name, item.Coverage)
		}
	}

	fmt.Fprintf(&b, "\n## Failures\n\n")
	if len(report.Failures) == 0 {
		fmt.Fprintf(&b, "No failed tests were reported.\n")
	} else {
		fmt.Fprintf(&b, "| Package | Test |\n| --- | --- |\n")
		for _, failure := range report.Failures {
			testName := failure.Test
			if testName == "" {
				testName = "(package)"
			}
			fmt.Fprintf(&b, "| `%s` | `%s` |\n", failure.Package, testName)
		}
	}

	return b.String()
}

func renderText(report testReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Test Report\n")
	fmt.Fprintf(&b, "Generated at: %s\n", report.GeneratedAt)
	fmt.Fprintf(&b, "Test exit code: %d\n", report.TestExitCode)
	fmt.Fprintf(&b, "Coverage file: %s\n", report.CoverageFile)
	fmt.Fprintf(&b, "Go test JSON log: %s\n", report.JSONLogFile)
	if report.TotalCoverage > 0 {
		fmt.Fprintf(&b, "Total coverage: %.1f%%\n", report.TotalCoverage)
	}
	if report.TestError != "" {
		fmt.Fprintf(&b, "Test command error: %s\n", report.TestError)
	}

	fmt.Fprintf(&b, "\nPackage coverage:\n")
	if len(report.PackageCoverages) == 0 {
		fmt.Fprintf(&b, "  none captured\n")
	} else {
		for _, item := range report.PackageCoverages {
			fmt.Fprintf(&b, "  %.1f%%  %s\n", item.Coverage, item.Package)
		}
	}

	fmt.Fprintf(&b, "\nLow coverage functions:\n")
	if len(report.LowCoverageFunctions) == 0 {
		fmt.Fprintf(&b, "  none below threshold\n")
	} else {
		for _, item := range report.LowCoverageFunctions {
			fmt.Fprintf(&b, "  %.1f%%  %s %s\n", item.Coverage, item.Location, item.Name)
		}
	}

	fmt.Fprintf(&b, "\nFailures:\n")
	if len(report.Failures) == 0 {
		fmt.Fprintf(&b, "  none\n")
	} else {
		for _, failure := range report.Failures {
			testName := failure.Test
			if testName == "" {
				testName = "(package)"
			}
			fmt.Fprintf(&b, "  %s %s\n", failure.Package, testName)
		}
	}

	return b.String()
}
