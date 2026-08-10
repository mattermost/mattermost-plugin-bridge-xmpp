package main

import (
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const syncPostDedupeTTL = 30 * time.Second

var (
	syncPostDedupeOnce sync.Once
	syncPostDedupe     *ttlcache.Cache[string, struct{}]
)

func getSyncPostDedupe() *ttlcache.Cache[string, struct{}] {
	syncPostDedupeOnce.Do(func() {
		syncPostDedupe = ttlcache.New(
			ttlcache.WithTTL[string, struct{}](syncPostDedupeTTL),
		)
		go syncPostDedupe.Start()
	})
	return syncPostDedupe
}

// MessageHasBeenPosted syncs local posts on mapped channels to XMPP.
// Shared Channels remains the preferred enterprise path; this hook covers
// environments where the Shared Channels service is unavailable (e.g. no license).
func (p *Plugin) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	if post == nil || post.Id == "" {
		return
	}

	config := p.getConfiguration()
	if !config.EnableSync || p.bridgeManager == nil {
		return
	}

	bridge, err := p.bridgeManager.GetBridge("xmpp")
	if err != nil || !bridge.IsConnected() {
		return
	}

	mapping, err := bridge.GetChannelMapping(post.ChannelId)
	if err != nil || mapping == "" {
		return
	}

	if err := p.processSyncPost(post, post.ChannelId, nil); err != nil {
		p.logger.LogError("Failed to process posted message for XMPP sync",
			"post_id", post.Id,
			"channel_id", post.ChannelId,
			"error", err)
	}
}

// markSyncPost handles cross-path dedupe between MessageHasBeenPosted and
// OnSharedChannelsSyncMsg. Returns false if the post was already processed.
func markSyncPost(postID string) bool {
	cache := getSyncPostDedupe()
	if cache.Has(postID) {
		return false
	}
	cache.Set(postID, struct{}{}, ttlcache.DefaultTTL)
	return true
}
