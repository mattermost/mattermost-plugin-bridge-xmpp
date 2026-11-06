package main

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

type nopLogger struct{}

func (nopLogger) LogDebug(string, ...any) {}
func (nopLogger) LogInfo(string, ...any)  {}
func (nopLogger) LogWarn(string, ...any)  {}
func (nopLogger) LogError(string, ...any) {}

type fakeXMPPBridge struct {
	pluginModel.Bridge
	connected bool
}

func (b *fakeXMPPBridge) IsConnected() bool { return b.connected }

type fakeBridgeManager struct {
	pluginModel.BridgeManager
	xmpp       *fakeXMPPBridge
	failAfter  int
	published  []string
	publishErr error
}

func (m *fakeBridgeManager) GetBridge(name string) (pluginModel.Bridge, error) {
	if name != "xmpp" || m.xmpp == nil {
		return nil, errors.New("not registered")
	}
	return m.xmpp, nil
}

func (m *fakeBridgeManager) PublishMessage(msg *pluginModel.DirectionalMessage) error {
	if m.publishErr != nil && len(m.published) >= m.failAfter {
		return m.publishErr
	}
	m.published = append(m.published, msg.MessageID)
	return nil
}

const testBotUserID = "botuserid"

func setupSyncPlugin(t *testing.T, connected bool) (*Plugin, *plugintest.API, *fakeBridgeManager) {
	t.Helper()

	api := &plugintest.API{}
	t.Cleanup(func() { api.AssertExpectations(t) })

	bm := &fakeBridgeManager{xmpp: &fakeXMPPBridge{connected: connected}}

	p := &Plugin{
		logger:        nopLogger{},
		botUserID:     testBotUserID,
		bridgeManager: bm,
		configuration: &config.Configuration{EnableSync: true},
	}
	p.SetAPI(api)

	return p, api, bm
}

func newSyncMsg(posts ...*model.Post) *model.SyncMsg {
	msg := model.NewSyncMsg(model.NewId())
	msg.Posts = posts
	return msg
}

func newLocalPost(api *plugintest.API) *model.Post {
	user := &model.User{Id: model.NewId(), Username: "user-" + model.NewId()[:6]}
	api.On("GetUser", user.Id).Return(user, nil)
	return &model.Post{Id: model.NewId(), UserId: user.Id, Message: "hello"}
}

func TestOnSharedChannelsSyncMsg(t *testing.T) {
	t.Run("all posts delivered", func(t *testing.T) {
		p, api, bm := setupSyncPlugin(t, true)
		post1 := newLocalPost(api)
		post2 := newLocalPost(api)

		resp, err := p.OnSharedChannelsSyncMsg(newSyncMsg(post1, post2), &model.RemoteCluster{})

		require.NoError(t, err)
		assert.Empty(t, resp.PostErrors)
		assert.Equal(t, []string{post1.Id, post2.Id}, bm.published)
	})

	t.Run("failed post is reported in PostErrors", func(t *testing.T) {
		p, api, bm := setupSyncPlugin(t, true)
		post1 := newLocalPost(api)
		failed := &model.Post{Id: model.NewId(), UserId: model.NewId()}
		api.On("GetUser", failed.UserId).Return(nil, model.NewAppError("GetUser", "app.user.get.app_error", nil, "", http.StatusInternalServerError))
		post3 := newLocalPost(api)

		resp, err := p.OnSharedChannelsSyncMsg(newSyncMsg(post1, failed, post3), &model.RemoteCluster{})

		require.NoError(t, err)
		assert.Equal(t, []string{failed.Id}, resp.PostErrors)
		assert.Equal(t, []string{post1.Id, post3.Id}, bm.published)
	})

	t.Run("disconnected XMPP fails the whole batch", func(t *testing.T) {
		p, _, bm := setupSyncPlugin(t, false)
		post1 := &model.Post{Id: model.NewId(), UserId: model.NewId()}
		post2 := &model.Post{Id: model.NewId(), UserId: model.NewId()}

		resp, err := p.OnSharedChannelsSyncMsg(newSyncMsg(post1, post2), &model.RemoteCluster{})

		require.Error(t, err)
		assert.Empty(t, resp.PostErrors)
		assert.Empty(t, bm.published)
	})

	t.Run("unregistered XMPP bridge fails the whole batch", func(t *testing.T) {
		p, _, bm := setupSyncPlugin(t, true)
		bm.xmpp = nil

		_, err := p.OnSharedChannelsSyncMsg(newSyncMsg(&model.Post{Id: model.NewId(), UserId: model.NewId()}), &model.RemoteCluster{})

		require.Error(t, err)
	})

	t.Run("message without posts succeeds while disconnected", func(t *testing.T) {
		p, _, _ := setupSyncPlugin(t, false)

		resp, err := p.OnSharedChannelsSyncMsg(newSyncMsg(), &model.RemoteCluster{})

		require.NoError(t, err)
		assert.Empty(t, resp.PostErrors)
	})

	t.Run("message bus failure before any delivery fails the whole batch", func(t *testing.T) {
		p, api, bm := setupSyncPlugin(t, true)
		botPost := &model.Post{Id: model.NewId(), UserId: testBotUserID}
		post2 := newLocalPost(api)
		bm.publishErr = errors.New("message delivery timeout")

		resp, err := p.OnSharedChannelsSyncMsg(newSyncMsg(botPost, post2), &model.RemoteCluster{})

		require.Error(t, err)
		assert.Empty(t, resp.PostErrors)
		assert.Empty(t, bm.published)
	})

	t.Run("message bus failure after a delivery reports the remaining posts", func(t *testing.T) {
		p, api, bm := setupSyncPlugin(t, true)
		post1 := newLocalPost(api)
		post2 := newLocalPost(api)
		post3 := &model.Post{Id: model.NewId(), UserId: model.NewId()}
		bm.publishErr = errors.New("message delivery timeout")
		bm.failAfter = 1

		resp, err := p.OnSharedChannelsSyncMsg(newSyncMsg(post1, post2, post3), &model.RemoteCluster{})

		require.NoError(t, err)
		assert.Equal(t, []string{post2.Id, post3.Id}, resp.PostErrors)
		assert.Equal(t, []string{post1.Id}, bm.published)
	})
}
