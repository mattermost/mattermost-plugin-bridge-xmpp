package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	xmpptestest "github.com/mattermost/mattermost-plugin-bridge-xmpp/testcontainers/xmpp"
)

// XMPPSyncTestSuite exercises Prosody-backed sync with a mocked Mattermost API.
type XMPPSyncTestSuite struct {
	suite.Suite

	prosody  *xmpptestest.Container
	occupant *xmpptestest.OccupantClient

	plugin         *Plugin
	api            *plugintest.API
	channelID      string
	teamID         string
	userID         string
	botUserID      string
	remoteID       string
	createdPosts   []*model.Post
	createdPostsMu sync.Mutex
}

func TestXMPPSyncIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping XMPP integration tests in short mode")
	}
	suite.Run(t, new(XMPPSyncTestSuite))
}

func (s *XMPPSyncTestSuite) SetupSuite() {
	s.prosody = xmpptestest.StartContainer(s.T(), xmpptestest.DefaultConfig())
}

func (s *XMPPSyncTestSuite) TearDownSuite() {
	if s.prosody != nil {
		s.prosody.Cleanup(s.T())
	}
}

func (s *XMPPSyncTestSuite) SetupTest() {
	log := newTestLogger(s.T())

	s.occupant = xmpptestest.ConnectOccupant(s.T(), s.prosody, "sync-occupant", log)
	require.NoError(s.T(), s.occupant.JoinRoom(s.prosody.Config.RoomJID))

	s.channelID = model.NewId()
	s.teamID = model.NewId()
	s.userID = model.NewId()
	s.botUserID = model.NewId()
	s.remoteID = "test-remote-id"
	s.createdPosts = nil

	s.api = &plugintest.API{}
	s.setupAPIMocks()

	cfg := &config.Configuration{
		XMPPServerURL:          s.prosody.HostURL,
		XMPPUsername:           s.prosody.Config.BridgeJID,
		XMPPPassword:           s.prosody.Config.BridgePassword,
		XMPPResource:           "mattermost-bridge",
		XMPPInsecureSkipVerify: true,
		EnableSync:             true,
		EnableXMPPGhostUsers:   false,
	}

	s.plugin = &Plugin{
		remoteID:  s.remoteID,
		botUserID: s.botUserID,
		logger:    log,
		kvstore:   NewMemoryKVStore(),
	}
	s.plugin.SetAPI(s.api)
	s.plugin.configuration = cfg

	s.plugin.bridgeManager = bridge.NewBridgeManager(log, s.api, s.remoteID)
	require.NoError(s.T(), s.plugin.initBridges(cfg))
	require.NoError(s.T(), s.plugin.bridgeManager.Start())

	for _, name := range s.plugin.bridgeManager.ListBridges() {
		require.NoError(s.T(), s.plugin.bridgeManager.StartBridge(name))
	}

	require.NoError(s.T(), s.plugin.bridgeManager.CreateChannelMapping(&pluginModel.CreateChannelMappingRequest{
		ChannelID:       s.channelID,
		BridgeName:      "xmpp",
		BridgeChannelID: s.prosody.Config.RoomJID,
		UserID:          s.userID,
		TeamID:          s.teamID,
	}))

	s.T().Cleanup(func() {
		if s.plugin.bridgeManager != nil {
			_ = s.plugin.bridgeManager.Shutdown()
		}
	})
}

func (s *XMPPSyncTestSuite) setupAPIMocks() {
	s.api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	s.api.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	s.api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	s.api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	testUser := &model.User{
		Id:       s.userID,
		Username: "mmuser",
		Email:    "mmuser@example.com",
	}
	s.api.On("GetUser", s.userID).Return(testUser, nil).Maybe()
	s.api.On("GetUser", mock.AnythingOfType("string")).Return(&model.User{Id: "other", Username: "other"}, nil).Maybe()

	s.api.On("GetChannel", s.channelID).Return(&model.Channel{
		Id:     s.channelID,
		TeamId: s.teamID,
		Name:   "sync-channel",
		Type:   model.ChannelTypeOpen,
	}, nil).Maybe()

	s.api.On("GetUserByUsername", mock.AnythingOfType("string")).Return(nil, model.NewAppError("GetUserByUsername", "app.user.get_by_username.app_error", nil, "not found", 404)).Maybe()
	s.api.On("GetUserByEmail", mock.AnythingOfType("string")).Return(nil, model.NewAppError("GetUserByEmail", "app.user.get_by_email.app_error", nil, "not found", 404)).Maybe()

	s.api.On("CreateUser", mock.AnythingOfType("*model.User")).Return(func(user *model.User) *model.User {
		created := *user
		created.Id = model.NewId()
		return &created
	}, nil).Maybe()

	s.api.On("InviteRemoteToChannel", s.channelID, s.remoteID, mock.AnythingOfType("string"), true).Return(nil).Maybe()

	s.api.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(func(post *model.Post) *model.Post {
		created := post.Clone()
		created.Id = model.NewId()
		created.CreateAt = time.Now().UnixMilli()
		s.createdPostsMu.Lock()
		s.createdPosts = append(s.createdPosts, created)
		s.createdPostsMu.Unlock()
		return created
	}, nil).Maybe()

	s.api.On("ShareChannel", mock.AnythingOfType("*model.SharedChannel")).Return(func(sc *model.SharedChannel) *model.SharedChannel {
		return sc
	}, nil).Maybe()
}

func (s *XMPPSyncTestSuite) TestTextBidirectionalSync() {
	s.Run("MattermostToXMPP", func() {
		s.occupant.ClearMessages()
		token := fmt.Sprintf("mm-to-xmpp-%d", time.Now().UnixNano())
		post := &model.Post{
			Id:        model.NewId(),
			UserId:    s.userID,
			ChannelId: s.channelID,
			Message:   token,
			CreateAt:  time.Now().UnixMilli(),
		}

		require.NoError(s.T(), s.plugin.processSyncPost(post, s.channelID, nil))

		msg, err := s.occupant.WaitForMessage(token, 30*time.Second)
		require.NoError(s.T(), err)
		require.Contains(s.T(), msg.Body, token)
	})

	s.Run("XMPPToMattermost", func() {
		s.createdPostsMu.Lock()
		s.createdPosts = nil
		s.createdPostsMu.Unlock()

		token := fmt.Sprintf("xmpp-to-mm-%d", time.Now().UnixNano())
		require.NoError(s.T(), s.occupant.SendGroupchat(s.prosody.Config.RoomJID, token))

		require.Eventually(s.T(), func() bool {
			s.createdPostsMu.Lock()
			defer s.createdPostsMu.Unlock()
			for _, post := range s.createdPosts {
				if post.ChannelId == s.channelID && post.Message == token {
					return true
				}
			}
			return false
		}, 30*time.Second, 200*time.Millisecond, "expected CreatePost with XMPP message body")
	})
}

func (s *XMPPSyncTestSuite) TestUnmappedChannelDoesNotSync() {
	s.occupant.ClearMessages()
	unmappedChannelID := model.NewId()
	token := fmt.Sprintf("unmapped-%d", time.Now().UnixNano())
	post := &model.Post{
		Id:        model.NewId(),
		UserId:    s.userID,
		ChannelId: unmappedChannelID,
		Message:   token,
		CreateAt:  time.Now().UnixMilli(),
	}

	// processSyncPost publishes regardless; XMPP handler should drop unmapped channels.
	_ = s.plugin.processSyncPost(post, unmappedChannelID, nil)
	require.NoError(s.T(), s.occupant.AssertNoMessage(token, 5*time.Second))
}
