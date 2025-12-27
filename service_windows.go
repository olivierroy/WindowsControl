//go:build windows

package main

import (
	"context"
	"errors"
	"log"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "WindowsControl"

type windowsService struct{}

func maybeRunService() (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false, err
	}
	if !isService {
		return false, nil
	}
	return true, svc.Run(serviceName, &windowsService{})
}

func (windowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx)
	}()

	status := svc.Status{State: svc.Running, Accepts: accepts}
	changes <- status

	for {
		select {
		case err := <-done:
			if err != nil {
				log.Printf("service server exited: %v", err)
			}
			status.State = svc.Stopped
			status.Accepts = 0
			changes <- status
			return false, 0
		case change := <-r:
			switch change.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				status.State = svc.StopPending
				status.Accepts = accepts
				changes <- status
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("shutdown error: %v", err)
				}
				status.State = svc.Stopped
				status.Accepts = 0
				changes <- status
				return true, 0
			default:
			}
		}
	}
}
