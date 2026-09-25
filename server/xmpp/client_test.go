package xmpp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type nopLogger struct{}

func (nopLogger) LogDebug(string, ...any) {}
func (nopLogger) LogInfo(string, ...any)  {}
func (nopLogger) LogWarn(string, ...any)  {}
func (nopLogger) LogError(string, ...any) {}

func newUnreachableClient(t *testing.T) *Client {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := l.Addr().String()
	_ = l.Close()

	return NewClient(closedAddr, "bridge@localhost", "secret", "test", "", nopLogger{})
}

func TestConnectAfterDisconnectIsNotCanceled(t *testing.T) {
	c := newUnreachableClient(t)
	c.cancel()

	err := c.Connect()
	if err == nil {
		t.Fatal("expected dial to a closed port to fail")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("Connect reused the canceled context: %v", err)
	}
}

func TestConnectKeepsLiveContext(t *testing.T) {
	c := newUnreachableClient(t)
	ctx := c.ctx

	_ = c.Connect()

	if c.ctx != ctx {
		t.Fatal("Connect replaced a context that was not canceled")
	}
}

// Expired items are only evicted by the cleanup loop, so an eviction proves it is running.
func dedupeCacheRunning(c *Client) bool {
	evicted := make(chan struct{}, 1)
	unsubscribe := c.dedupeCache.OnEviction(func(context.Context, ttlcache.EvictionReason, *ttlcache.Item[string, time.Time]) {
		evicted <- struct{}{}
	})
	defer unsubscribe()
	c.dedupeCache.Set("probe", time.Now(), 10*time.Millisecond)

	select {
	case <-evicted:
		return true
	case <-time.After(time.Second):
		return false
	}
}

func TestConnectRestartsDedupeCache(t *testing.T) {
	c := newUnreachableClient(t)
	if !dedupeCacheRunning(c) {
		t.Fatal("dedupe cache cleanup not running after NewClient")
	}
	c.dedupeCache.Stop()

	_ = c.Connect()

	if !dedupeCacheRunning(c) {
		t.Fatal("dedupe cache cleanup did not restart after Connect")
	}
}
