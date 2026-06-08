//go:build !windows

package main

import "context"

func runAsWindowsService(run func(context.Context) error) (bool, error) {
	return false, nil
}
