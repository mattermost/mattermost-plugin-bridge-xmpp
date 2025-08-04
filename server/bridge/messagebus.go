package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

const (
	// DefaultMessageBufferSize is the default buffer size for message channels
	DefaultMessageBufferSize = 1000
	
	// MessageDeliveryTimeout is the maximum time to wait for message delivery
	MessageDeliveryTimeout = 5 * time.Second
)

// messageBus implements the MessageBus interface
type messageBus struct {
	// Core messaging
	incomingMessages chan *model.DirectionalMessage
	subscribers      map[string]chan *model.DirectionalMessage
	subscribersMu    sync.RWMutex
	
	// Lifecycle management
	ctx      context.Context
	cancel   context.CancelFunc
	logger   logger.Logger
	wg       sync.WaitGroup
	started  bool
	startMu  sync.Mutex
}

// NewMessageBus creates a new message bus instance
func NewMessageBus(logger logger.Logger) model.MessageBus {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &messageBus{
		incomingMessages: make(chan *model.DirectionalMessage, DefaultMessageBufferSize),
		subscribers:      make(map[string]chan *model.DirectionalMessage),
		ctx:              ctx,
		cancel:           cancel,
		logger:           logger,
	}
}

// Subscribe returns a channel that receives messages for the specified bridge
func (mb *messageBus) Subscribe(bridgeName string) <-chan *model.DirectionalMessage {
	mb.subscribersMu.Lock()
	defer mb.subscribersMu.Unlock()
	
	// Create a buffered channel for this subscriber
	ch := make(chan *model.DirectionalMessage, DefaultMessageBufferSize)
	mb.subscribers[bridgeName] = ch
	
	mb.logger.LogDebug("Bridge subscribed to message bus", "bridge", bridgeName)
	return ch
}

// Publish sends a message to the message bus for routing
func (mb *messageBus) Publish(msg *model.DirectionalMessage) error {
	if msg == nil {
		return fmt.Errorf("message cannot be nil")
	}
	
	if msg.BridgeMessage == nil {
		return fmt.Errorf("bridge message cannot be nil")
	}
	
	select {
	case mb.incomingMessages <- msg:
		mb.logger.LogDebug("Message published to bus", 
			"source_bridge", msg.SourceBridge,
			"direction", msg.Direction,
			"channel_id", msg.SourceChannelID)
		return nil
	case <-time.After(MessageDeliveryTimeout):
		mb.logger.LogWarn("Message delivery timeout", 
			"source_bridge", msg.SourceBridge,
			"channel_id", msg.SourceChannelID)
		return fmt.Errorf("message delivery timeout")
	case <-mb.ctx.Done():
		return fmt.Errorf("message bus is shutting down")
	}
}

// Start begins message routing
func (mb *messageBus) Start() error {
	mb.startMu.Lock()
	defer mb.startMu.Unlock()
	
	if mb.started {
		return fmt.Errorf("message bus is already started")
	}
	
	mb.logger.LogInfo("Starting message bus")
	
	// Start the message routing goroutine
	mb.wg.Add(1)
	go mb.routeMessages()
	
	mb.started = true
	mb.logger.LogInfo("Message bus started successfully")
	return nil
}

// Stop ends message routing and cleans up resources
func (mb *messageBus) Stop() error {
	mb.startMu.Lock()
	defer mb.startMu.Unlock()
	
	if !mb.started {
		return nil // Already stopped
	}
	
	mb.logger.LogInfo("Stopping message bus")
	
	// Cancel context to signal shutdown
	mb.cancel()
	
	// Wait for routing goroutine to finish
	mb.wg.Wait()
	
	// Close all subscriber channels
	mb.subscribersMu.Lock()
	for bridgeName, ch := range mb.subscribers {
		close(ch)
		mb.logger.LogDebug("Closed subscriber channel", "bridge", bridgeName)
	}
	mb.subscribers = make(map[string]chan *model.DirectionalMessage)
	mb.subscribersMu.Unlock()
	
	// Close incoming messages channel
	close(mb.incomingMessages)
	
	mb.started = false
	mb.logger.LogInfo("Message bus stopped successfully")
	return nil
}

// routeMessages handles the main message routing loop
func (mb *messageBus) routeMessages() {
	defer mb.wg.Done()
	
	mb.logger.LogDebug("Message routing started")
	
	for {
		select {
		case msg, ok := <-mb.incomingMessages:
			if !ok {
				mb.logger.LogDebug("Incoming messages channel closed, stopping routing")
				return
			}
			
			if err := mb.routeMessage(msg); err != nil {
				mb.logger.LogError("Failed to route message", 
					"source_bridge", msg.SourceBridge,
					"direction", msg.Direction,
					"error", err)
			}
			
		case <-mb.ctx.Done():
			mb.logger.LogDebug("Context cancelled, stopping message routing")
			return
		}
	}
}

// routeMessage routes a single message to appropriate subscribers
func (mb *messageBus) routeMessage(msg *model.DirectionalMessage) error {
	mb.subscribersMu.RLock()
	defer mb.subscribersMu.RUnlock()
	
	routedCount := 0
	
	// Route to specific target bridges if specified
	if len(msg.TargetBridges) > 0 {
		for _, targetBridge := range msg.TargetBridges {
			if ch, exists := mb.subscribers[targetBridge]; exists {
				if mb.deliverMessage(ch, msg, targetBridge) {
					routedCount++
				}
			} else {
				mb.logger.LogWarn("Target bridge not subscribed", 
					"target_bridge", targetBridge,
					"source_bridge", msg.SourceBridge)
			}
		}
	} else {
		// Route to all subscribers except the source bridge
		for bridgeName, ch := range mb.subscribers {
			if bridgeName != msg.SourceBridge {
				if mb.deliverMessage(ch, msg, bridgeName) {
					routedCount++
				}
			}
		}
	}
	
	mb.logger.LogDebug("Message routed", 
		"source_bridge", msg.SourceBridge,
		"routed_to_count", routedCount)
	
	return nil
}

// deliverMessage attempts to deliver a message to a specific subscriber
func (mb *messageBus) deliverMessage(ch chan *model.DirectionalMessage, msg *model.DirectionalMessage, targetBridge string) bool {
	select {
	case ch <- msg:
		return true
	case <-time.After(MessageDeliveryTimeout):
		mb.logger.LogWarn("Message delivery timeout to bridge", 
			"target_bridge", targetBridge,
			"source_bridge", msg.SourceBridge)
		return false
	case <-mb.ctx.Done():
		return false
	}
}

// GetStats returns statistics about the message bus
func (mb *messageBus) GetStats() map[string]interface{} {
	mb.subscribersMu.RLock()
	defer mb.subscribersMu.RUnlock()
	
	stats := map[string]interface{}{
		"started":            mb.started,
		"subscriber_count":   len(mb.subscribers),
		"buffer_size":        DefaultMessageBufferSize,
		"pending_messages":   len(mb.incomingMessages),
	}
	
	subscribers := make([]string, 0, len(mb.subscribers))
	for bridgeName := range mb.subscribers {
		subscribers = append(subscribers, bridgeName)
	}
	stats["subscribers"] = subscribers
	
	return stats
}