package vpngate

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/store"
)

type Refresher struct {
	config   *config.Store
	store    *store.Store
	client   *Client
	logger   *log.Logger
	trigger  chan struct{}
	mu       sync.Mutex
	stateMu  sync.RWMutex
	running  bool
	lastErr  error
	activeIP func() string
	after    func(context.Context)
}

func NewRefresher(configStore *config.Store, database *store.Store, logger *log.Logger, activeIP func() string, after func(context.Context)) *Refresher {
	return &Refresher{config: configStore, store: database, client: NewClient(), logger: logger, trigger: make(chan struct{}, 1), activeIP: activeIP, after: after}
}

func (r *Refresher) Trigger() bool {
	select {
	case r.trigger <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *Refresher) Running() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.running
}

func (r *Refresher) LastError() error {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.lastErr
}

func (r *Refresher) Run(ctx context.Context) {
	for {
		settings := r.config.Get()
		interval, _ := settings.RefreshDuration()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.trigger:
			timer.Stop()
		case <-timer.C:
		}
		if err := r.Refresh(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Printf("[refresh] %v", err)
		}
	}
}

func (r *Refresher) Refresh(ctx context.Context) error {
	if !r.mu.TryLock() {
		return errors.New("node refresh is already running")
	}
	defer r.mu.Unlock()
	r.setState(true, nil)
	settings := r.config.Get()
	nodes, err := r.client.Fetch(ctx, settings.SourceURL, settings.FilterExpression)
	if err == nil {
		refreshedAt := time.Now()
		for index := range nodes {
			nodes[index].RefreshedAt = refreshedAt
		}
		preserveIP := ""
		if r.activeIP != nil {
			preserveIP = r.activeIP()
		}
		err = r.store.ReplaceNodes(ctx, nodes, refreshedAt, preserveIP)
		if err == nil {
			r.logger.Printf("[refresh] stored %d filtered nodes", len(nodes))
			if r.after != nil {
				r.after(ctx)
			}
		}
	}
	r.setState(false, err)
	return err
}

func (r *Refresher) setState(running bool, err error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.running = running
	r.lastErr = err
}
