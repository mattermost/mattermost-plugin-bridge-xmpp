package main

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

// OnSharedChannelsPing is called to check if the bridge is healthy and ready to process messages
func (p *Plugin) OnSharedChannelsPing(remoteCluster *model.RemoteCluster) bool {
	config := p.getConfiguration()

	var remoteClusterID string
	if remoteCluster != nil {
		remoteClusterID = remoteCluster.RemoteId
	}

	p.logger.LogDebug("Received shared channels ping", "remote_cluster_id", remoteClusterID)

	// If sync is disabled, we're still "healthy" but not actively processing
	if !config.EnableSync {
		p.logger.LogDebug("Ping received but sync is disabled", "remote_cluster_id", remoteClusterID)
		return true
	}

	// Check if bridge manager is available
	if p.bridgeManager == nil {
		p.logger.LogError("Bridge manager not initialized during ping", "remote_cluster_id", remoteClusterID)
		return false
	}

	// Get the XMPP bridge for active connectivity testing
	bridge, err := p.bridgeManager.GetBridge("xmpp")
	if err != nil {
		p.logger.LogWarn("XMPP bridge not available during ping", "error", err, "remote_cluster_id", remoteClusterID)
		// The bridge is briefly unregistered during startup and shutdown. Reporting
		// unhealthy there marks the remote offline, and a channel mapped while the
		// remote is offline is stored as a pending invite that never syncs.
		return true
	}

	// Perform active ping test on the XMPP bridge
	if err := bridge.Ping(); err != nil {
		if p.notePingFailure() {
			p.logger.LogWarn("XMPP bridge ping failed, still inside the grace period",
				"error", err, "remote_cluster_id", remoteClusterID)
			return true
		}
		p.logger.LogError("XMPP bridge ping failing beyond the grace period",
			"error", err, "remote_cluster_id", remoteClusterID)
		return false
	}
	p.notePingSuccess()

	p.logger.LogDebug("Shared channels ping successful - XMPP bridge is healthy", "remote_cluster_id", remoteClusterID)
	return true
}

// pingFailureGracePeriod is how long the XMPP connection may be failing before the
// bridge reports itself unhealthy.
//
// Unlike an HTTP-based bridge, the XMPP client holds a long-lived session that the
// plugin itself tears down and rebuilds on every configuration change. Reporting
// unhealthy during those few seconds marks the remote offline, and a channel mapped
// while the remote is offline is stored as a pending invite that never syncs and is
// never retried. Kept well inside the server's own five minute
// RemoteOfflineAfterMillis so a genuine outage still surfaces.
//
// ponytail: one global window for the whole plugin; track it per remote if the plugin
// ever registers more than one XMPP server.
const pingFailureGracePeriod = 2 * time.Minute

// notePingFailure records a failed health check and reports whether the failure is
// still recent enough to keep claiming the bridge is healthy.
func (p *Plugin) notePingFailure() bool {
	p.pingMu.Lock()
	defer p.pingMu.Unlock()

	if p.pingFailingSince.IsZero() {
		p.pingFailingSince = time.Now()
	}
	return time.Since(p.pingFailingSince) < pingFailureGracePeriod
}

// notePingSuccess clears any recorded failure streak.
func (p *Plugin) notePingSuccess() {
	p.pingMu.Lock()
	defer p.pingMu.Unlock()

	p.pingFailingSince = time.Time{}
}

// OnSharedChannelsSyncMsg processes sync messages from Mattermost shared channels and routes them to XMPP
func (p *Plugin) OnSharedChannelsSyncMsg(msg *model.SyncMsg, rc *model.RemoteCluster) (model.SyncResponse, error) {
	var remoteClusterID string
	if rc != nil {
		remoteClusterID = rc.RemoteId
	}

	config := p.getConfiguration()

	// Initialize sync response
	now := model.GetMillis()
	response := model.SyncResponse{
		PostsLastUpdateAt:     now,
		UsersLastUpdateAt:     now,
		ReactionsLastUpdateAt: now,
	}

	p.logger.LogDebug("OnSharedChannelsSyncMsg called",
		"remote_cluster_id", remoteClusterID,
		"channel_id", msg.ChannelId,
		"post_count", len(msg.Posts))

	// If sync is disabled, return success but don't process
	if !config.EnableSync {
		p.logger.LogDebug("Sync message received but sync is disabled", "remote_cluster_id", remoteClusterID)
		return response, nil
	}

	// Process each post in the sync message
	var processedCount int
	var errors []string

	for _, post := range msg.Posts {
		if err := p.processSyncPost(post, msg.ChannelId, msg.Users); err != nil {
			errorMsg := fmt.Sprintf("failed to process post %s: %v", post.Id, err)
			errors = append(errors, errorMsg)
			p.logger.LogError("Failed to process sync post", "post_id", post.Id, "error", err)
		} else {
			processedCount++
		}
	}

	p.logger.LogInfo("Processed sync message",
		"remote_cluster_id", remoteClusterID,
		"channel_id", msg.ChannelId,
		"processed_posts", processedCount,
		"failed_posts", len(errors))

	// If we have errors, return them
	if len(errors) > 0 {
		return response, fmt.Errorf("failed to process %d posts: %v", len(errors), errors)
	}

	return response, nil
}

// processSyncPost converts a Mattermost post to a bridge message and routes it to XMPP
func (p *Plugin) processSyncPost(post *model.Post, channelID string, users map[string]*model.User) error {
	p.logger.LogDebug("Processing sync post", "post_id", post.Id, "channel_id", channelID, "users", users)

	// Skip messages from our own bot user to prevent loops
	if post.UserId == p.botUserID {
		p.logger.LogDebug("Skipping message from bot user to prevent loop",
			"bot_user_id", p.botUserID,
			"post_user_id", post.UserId)
		return nil
	}

	// Skip messages from remote users to prevent loops
	// Remote users represent users from other bridges (e.g., XMPP users in Mattermost)
	user, appErr := p.API.GetUser(post.UserId)
	if appErr != nil {
		p.logger.LogWarn("Failed to get user details for loop prevention. Ignoring message.", "user_id", post.UserId, "error", appErr)
		return nil
	} else if user != nil && user.RemoteId != nil && *user.RemoteId != "" {
		p.logger.LogDebug("Skipping message from remote user to prevent loop",
			"user_id", post.UserId,
			"username", user.Username,
			"remote_id", *user.RemoteId)
		return nil
	}

	// Find the user who created this post
	var postUser *model.User
	p.logger.LogInfo("Processing sync post", "post_id", post.UserId, "users", users)
	if users != nil {
		postUser = users[post.UserId]
	}

	// If user not found in sync data, try to get from API
	if postUser == nil {
		var appErr *model.AppError
		postUser, appErr = p.API.GetUser(post.UserId)
		if appErr != nil {
			p.logger.LogWarn("Failed to get user for post", "user_id", post.UserId, "post_id", post.Id, "error", appErr)
			// Create a placeholder user
			postUser = &model.User{
				Id:       post.UserId,
				Username: "unknown-user",
			}
		}
	}

	// Create bridge message from Mattermost post
	bridgeMessage := &pluginModel.BridgeMessage{
		SourceBridge:    "mattermost",
		SourceChannelID: channelID,
		SourceUserID:    postUser.Id,
		SourceUserName:  postUser.Username,
		SourceRemoteID:  "", // This message comes from Mattermost, so no remote ID
		Content:         post.Message,
		MessageType:     "text", // TODO: Handle other message types
		Timestamp:       time.Unix(post.CreateAt/1000, 0),
		MessageID:       post.Id,
		ThreadID:        post.RootId,
		TargetBridges:   []string{"xmpp"}, // Route to XMPP
		Metadata: map[string]any{
			"original_post": post,
			"channel_id":    channelID,
		},
	}

	// Create directional message for outgoing (Mattermost -> XMPP)
	directionalMessage := &pluginModel.DirectionalMessage{
		BridgeMessage: bridgeMessage,
		Direction:     pluginModel.DirectionOutgoing,
	}

	// Publish the message to the message bus for routing to XMPP bridge
	if err := p.bridgeManager.PublishMessage(directionalMessage); err != nil {
		return fmt.Errorf("failed to publish sync message to message bus: %w", err)
	}

	p.logger.LogDebug("Successfully published sync message to message bus",
		"post_id", post.Id,
		"channel_id", channelID,
		"user", postUser.Username)

	return nil
}
