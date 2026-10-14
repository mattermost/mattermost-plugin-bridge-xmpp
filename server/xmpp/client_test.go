package xmpp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
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

// Nothing sweeps the dedupe cache in the background, so Has must reject an expired
// entry on its own. Deduplication would otherwise drop real messages forever.
func TestDedupeCacheExpiresWithoutCleanupLoop(t *testing.T) {
	c := newUnreachableClient(t)

	c.dedupeCache.Set("msg-1", time.Now(), 10*time.Millisecond)
	if !c.dedupeCache.Has("msg-1") {
		t.Fatal("a message id just stored should count as a duplicate")
	}

	time.Sleep(50 * time.Millisecond)

	if c.dedupeCache.Has("msg-1") {
		t.Fatal("an expired message id should no longer count as a duplicate")
	}
}
