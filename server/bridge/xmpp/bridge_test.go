package xmpp

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

type nopLogger struct{}

func (nopLogger) LogDebug(string, ...any) {}
func (nopLogger) LogInfo(string, ...any)  {}
func (nopLogger) LogWarn(string, ...any)  {}
func (nopLogger) LogError(string, ...any) {}

type spyUserManager struct {
	pluginModel.BridgeUserManager
	stops int
}

func (s *spyUserManager) Stop() error {
	s.stops++
	return nil
}

func newTestBridge(cfg *config.Configuration) *xmppBridge {
	return NewBridge(nopLogger{}, nil, nil, cfg, "xmpp", "", "").(*xmppBridge)
}

func TestUpdateConfigurationSkipsRestartWhenUnchanged(t *testing.T) {
	b := newTestBridge(&config.Configuration{XMPPResource: "a"})
	b.connected.Store(true)
	spy := &spyUserManager{}
	b.userManager = spy

	same := &config.Configuration{XMPPResource: "a"}
	if err := b.UpdateConfiguration(same); err != nil {
		t.Fatal(err)
	}

	if b.userManager != spy || spy.stops != 0 {
		t.Fatal("bridge restarted for an unchanged configuration")
	}
	if b.getConfiguration() != same {
		t.Fatal("configuration was not stored")
	}
}

func TestUpdateConfigurationRestartsDisconnectedBridge(t *testing.T) {
	b := newTestBridge(&config.Configuration{XMPPResource: "a"})
	spy := &spyUserManager{}
	b.userManager = spy

	if err := b.UpdateConfiguration(&config.Configuration{XMPPResource: "a"}); err != nil {
		t.Fatal(err)
	}

	if spy.stops != 1 {
		t.Fatal("disconnected bridge was not restarted on an unchanged configuration")
	}
}

func TestUpdateConfigurationRecreatesClientWhenChanged(t *testing.T) {
	b := newTestBridge(&config.Configuration{XMPPResource: "a"})

	if err := b.UpdateConfiguration(&config.Configuration{XMPPResource: "b"}); err != nil {
		t.Fatal(err)
	}

	if b.bridgeClient == nil {
		t.Fatal("changed configuration did not recreate the XMPP client")
	}
}

func TestConnectionMonitorStopsWithItsContext(t *testing.T) {
	b := newTestBridge(&config.Configuration{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		b.connectionMonitor(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connectionMonitor kept running after its context was canceled")
	}
}

func TestHandleReconnectionStopsWithItsContext(t *testing.T) {
	b := newTestBridge(&config.Configuration{EnableSync: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	b.handleReconnection(ctx)

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("handleReconnection ignored its canceled context, took %s", elapsed)
	}
}
