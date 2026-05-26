package router

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	selfUpdateTimeout         = 15 * time.Minute
	selfUpdateOutputLimit     = 4000
	selfUpdateRestartMarker   = "WEIBO_AI_BRIDGE_RESTART_SCHEDULED=1"
	selfUpdateUncertainMarker = "WEIBO_AI_BRIDGE_RESTART_UNCERTAIN=1"
	selfUpdateCurrentMarker   = "WEIBO_AI_BRIDGE_ALREADY_UP_TO_DATE=1"
)

type shellSelfUpdater struct {
	scriptPath string
	timeout    time.Duration
}

func newShellSelfUpdater() selfUpdater {
	return &shellSelfUpdater{timeout: selfUpdateTimeout}
}

func (u *shellSelfUpdater) Run(args []string) (selfUpdateResult, error) {
	scriptPath, err := u.resolveScriptPath()
	if err != nil {
		return selfUpdateResult{}, err
	}

	timeout := u.timeout
	if timeout <= 0 {
		timeout = selfUpdateTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.CommandContext(ctx, "bash", cmdArgs...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(os.Getenv("WEIBO_AI_BRIDGE_RESTART_DELAY")) == "" {
		cmd.Env = append(cmd.Env, "WEIBO_AI_BRIDGE_RESTART_DELAY=8")
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err = cmd.Run()
	text := cleanSelfUpdateOutput(limitSelfUpdateOutput(output.String()))
	result := selfUpdateResult{
		Output:           text,
		RestartScheduled: strings.Contains(output.String(), selfUpdateRestartMarker),
		AlreadyUpToDate:  strings.Contains(output.String(), selfUpdateCurrentMarker),
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, ctx.Err()
	}
	return result, err
}

func (u *shellSelfUpdater) resolveScriptPath() (string, error) {
	if strings.TrimSpace(u.scriptPath) != "" {
		return u.scriptPath, nil
	}

	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "scripts", "self-update.sh"))
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "scripts", "self-update.sh"))
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", errors.New("self-update script not found: scripts/self-update.sh")
}

func limitSelfUpdateOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) <= selfUpdateOutputLimit {
		return output
	}

	runes := []rune(output)
	if len(runes) <= selfUpdateOutputLimit {
		return output
	}
	return "...(output truncated)\n" + string(runes[len(runes)-selfUpdateOutputLimit:])
}

func cleanSelfUpdateOutput(output string) string {
	lines := strings.Split(output, "\n")
	kept := lines[:0]
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case selfUpdateRestartMarker, selfUpdateUncertainMarker, selfUpdateCurrentMarker:
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
