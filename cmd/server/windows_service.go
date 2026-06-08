//go:build windows

package main

import (
	"context"
	"os"
	"strings"

	"golang.org/x/sys/windows/svc"
)

type bridgeWindowsService struct {
	run func(context.Context) error
}

func runAsWindowsService(run func(context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false, err
	}
	if !isService {
		return false, nil
	}

	name := strings.TrimSpace(os.Getenv("WEIBO_AI_BRIDGE_SERVICE_NAME"))
	if name == "" {
		name = "weibo-ai-bridge"
	}
	return true, svc.Run(name, &bridgeWindowsService{run: run})
}

func (s *bridgeWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- svc.Status{State: svc.Running, Accepts: accepted}
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				if err := <-done; err != nil {
					return false, 1
				}
				return false, 0
			default:
				changes <- svc.Status{State: svc.Running, Accepts: accepted}
			}
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
