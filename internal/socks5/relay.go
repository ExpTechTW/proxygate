package socks5

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

type activityWriter struct {
	writer  io.Writer
	last    *atomic.Int64
	started time.Time
}

func (w activityWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if n > 0 {
		w.last.Store(time.Since(w.started).Nanoseconds())
	}
	return n, err
}

func relayTCP(ctx context.Context, client, upstream net.Conn, idleTimeout, halfCloseTimeout time.Duration) error {
	var lastActivity atomic.Int64
	started := time.Now()
	results := make(chan error, 2)
	completed := 0
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		// Closing both sockets unblocks reads and writes in either direction.
		for completed < 2 {
			<-results
			completed++
		}
	}()
	copyStream := func(destination, source net.Conn) {
		_, err := io.Copy(activityWriter{writer: destination, last: &lastActivity, started: started}, source)
		if err == nil {
			if closer, ok := destination.(interface{ CloseWrite() error }); ok {
				err = closer.CloseWrite()
			}
		}
		results <- err
	}
	go copyStream(upstream, client)
	go copyStream(client, upstream)

	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	var halfClosed <-chan time.Time
	var halfCloseTimer *time.Timer
	for completed < 2 {
		select {
		case <-ctx.Done():
			return nil
		case err := <-results:
			completed++
			if err != nil {
				return fmt.Errorf("relay TCP: %w", err)
			}
			if completed == 1 {
				// EOF only closes the write half. Allow the peer to finish its
				// response. Reclaim it if the remaining direction stops making
				// progress, without truncating an active download.
				lastActivity.Store(time.Since(started).Nanoseconds())
				halfCloseTimer = time.NewTimer(halfCloseTimeout)
				defer halfCloseTimer.Stop()
				halfClosed = halfCloseTimer.C
			}
		case <-halfClosed:
			remaining := halfCloseTimeout - (time.Since(started) - time.Duration(lastActivity.Load()))
			if remaining <= 0 {
				return fmt.Errorf("TCP half-close timeout after %s without progress", halfCloseTimeout)
			}
			halfCloseTimer.Reset(remaining)
		case <-idle.C:
			remaining := idleTimeout - (time.Since(started) - time.Duration(lastActivity.Load()))
			if remaining <= 0 {
				return fmt.Errorf("TCP idle timeout after %s", idleTimeout)
			}
			idle.Reset(remaining)
		}
	}
	return nil
}
