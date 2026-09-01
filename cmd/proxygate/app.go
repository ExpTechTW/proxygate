package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/server"
	"github.com/ExpTechTW/proxygate/internal/service"
	service_healthcheck "github.com/ExpTechTW/proxygate/internal/service/healthcheck"
	service_socks5 "github.com/ExpTechTW/proxygate/internal/service/socks5"
	service_speedtest "github.com/ExpTechTW/proxygate/internal/service/speedtest"
	"github.com/ExpTechTW/proxygate/internal/store"
	"github.com/ExpTechTW/proxygate/internal/vpn"
	"github.com/ExpTechTW/proxygate/internal/vpngate"
)

func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return path, nil
}

func appStart(args arguments, logger *log.Logger) error {
	configStore, err := config.Load(args.configPath)
	if err != nil {
		return err
	}
	database, err := store.Open(configStore.Get().DatabasePath)
	if err != nil {
		return fmt.Errorf("open SQLite database: %w", err)
	}
	restartExecutable, err := executablePath()
	if err != nil {
		_ = database.Close()
		return err
	}

	appCtx, cancelApp := context.WithCancel(context.Background())
	manager := vpn.NewManager(appCtx, configStore, database, logger, vpn.DefaultDrivers())
	refresher := vpngate.NewRefresher(configStore, database, logger, manager.ActiveIP, manager.AfterRefresh)
	socksService := service_socks5.New(configStore, manager, logger)
	speedTestService := service_speedtest.New(manager, logger)
	healthCheckService := service_healthcheck.New(configStore, manager, logger)
	services := []service.Service{socksService, speedTestService, healthCheckService}

	started := make([]service.Service, 0, len(services))
	for _, backgroundService := range services {
		if err := backgroundService.Start(); err != nil {
			cancelApp()
			_ = stopServices(context.Background(), started, logger)
			_ = manager.Close()
			_ = database.Close()
			return fmt.Errorf("start service %s: %w", backgroundService.Name(), err)
		}
		started = append(started, backgroundService)
		logger.Printf("[service:%s] started", backgroundService.Name())
	}

	var background sync.WaitGroup
	background.Add(2)
	go func() {
		defer background.Done()
		if err := manager.Start(appCtx); err != nil && appCtx.Err() == nil {
			logger.Printf("[vpn] initial connection: %v", err)
		}
	}()
	go func() {
		defer background.Done()
		refresher.Run(appCtx)
	}()

	lastRefresh := database.LastRefresh(appCtx)
	refreshInterval, _ := configStore.Get().RefreshDuration()
	if lastRefresh.IsZero() || time.Since(lastRefresh) >= refreshInterval {
		refresher.Trigger()
	}

	restartRequest := make(chan struct{}, 1)
	httpServer := server.New(configStore, database, manager, refresher, speedTestService, services, restartRequest, logger)
	httpServer.Setup()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- httpServer.Start() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, terminationSignals()...)
	defer signal.Stop(signals)

	reason := exitInterrupt
	var runErr error
	select {
	case <-signals:
		logger.Printf("[app] interrupt received, shutting down")
	case <-restartRequest:
		reason = exitRestart
		logger.Printf("[app] restart requested, shutting down")
	case err := <-serverErrors:
		reason = exitServerError
		if err == nil {
			err = errors.New("HTTP server stopped unexpectedly")
		}
		runErr = err
		logger.Printf("[app] %v", err)
	}

	cancelApp()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()

	var shutdownErrors []error
	if err := httpServer.Stop(shutdownCtx); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}
	if err := stopServices(shutdownCtx, started, logger); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}

	backgroundDone := make(chan struct{})
	go func() {
		background.Wait()
		close(backgroundDone)
	}()
	select {
	case <-backgroundDone:
	case <-shutdownCtx.Done():
		shutdownErrors = append(shutdownErrors, fmt.Errorf("stop background tasks: %w", shutdownCtx.Err()))
	}
	if err := manager.Close(); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("close VPN manager: %w", err))
	}
	if err := database.Close(); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("close database: %w", err))
	}
	if err := errors.Join(shutdownErrors...); err != nil {
		return errors.Join(runErr, err)
	}

	if reason == exitRestart {
		logger.Printf("[app] restarting %s", restartExecutable)
		return executeBinary(restartExecutable)
	}
	if reason == exitServerError {
		return runErr
	}
	return errInterrupted
}

func stopServices(ctx context.Context, services []service.Service, logger *log.Logger) error {
	var stopErrors []error
	for index := len(services) - 1; index >= 0; index-- {
		backgroundService := services[index]
		if err := backgroundService.Stop(ctx); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop service %s: %w", backgroundService.Name(), err))
			continue
		}
		logger.Printf("[service:%s] stopped", backgroundService.Name())
	}
	return errors.Join(stopErrors...)
}
