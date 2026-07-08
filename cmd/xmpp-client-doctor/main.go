package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

const (
	// Default values for development server (sidecar)
	defaultServer   = "localhost:5222"
	defaultUsername = "admin@localhost"
	defaultPassword = "admin"
	defaultResource = "doctor"
	defaultTestRoom = "test1@conference.localhost"
)

type Config struct {
	Server             string
	Username           string
	Password           string
	Resource           string
	TestRoom           string
	TestMUC            bool
	TestDirectMessage  bool
	TestRoomExists     bool
	TestXEP0077        bool
	Verbose            bool
	InsecureSkipVerify bool
}

func main() {
	config := &Config{}

	// Define command line flags
	flag.StringVar(&config.Server, "server", defaultServer, "XMPP server address (host:port)")
	flag.StringVar(&config.Username, "username", defaultUsername, "XMPP username/JID")
	flag.StringVar(&config.Password, "password", defaultPassword, "XMPP password")
	flag.StringVar(&config.Resource, "resource", defaultResource, "XMPP resource")
	flag.StringVar(&config.TestRoom, "test-room", defaultTestRoom, "MUC room JID for testing")
	flag.BoolVar(&config.TestMUC, "test-muc", true, "Enable MUC room testing (join/wait/leave)")
	flag.BoolVar(&config.TestDirectMessage, "test-dm", true, "Enable direct message testing (send message to admin user)")
	flag.BoolVar(&config.TestRoomExists, "test-room-exists", true, "Enable room existence testing using disco#info")
	flag.BoolVar(&config.TestXEP0077, "test-xep0077", true, "Enable XEP-0077 In-Band Registration testing with ghost user message test")
	flag.BoolVar(&config.Verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&config.InsecureSkipVerify, "insecure-skip-verify", true, "Skip TLS certificate verification (for development)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "xmpp-client-doctor - Test XMPP client connectivity and MUC operations\n\n")
		fmt.Fprintf(os.Stderr, "This tool tests the XMPP client implementation by connecting to an XMPP server,\n")
		fmt.Fprintf(os.Stderr, "performing connection tests, room existence checks, optionally testing MUC room operations\n")
		fmt.Fprintf(os.Stderr, "and direct messages, and then disconnecting gracefully.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s                    # Test basic connectivity\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --test-muc         # Test connectivity and MUC operations\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --test-dm          # Test connectivity and direct messages\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --test-muc=false --test-dm=false   # Test connectivity only\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDefault values are configured for the development server in ./sidecar/\n")
		fmt.Fprintf(os.Stderr, "Make sure to start the development server with: cd sidecar && docker-compose up -d\n")
		fmt.Fprintf(os.Stderr, "For MUC testing, create the test room 'test1' via the admin console at http://localhost:9090\n")
	}

	flag.Parse()

	// Create the main logger
	mainLogger := NewStructuredLogger(config.Verbose)

	mainLogger.LogInfo("Starting XMPP client doctor")
	mainLogger.LogInfo("Configuration",
		"server", config.Server,
		"username", config.Username,
		"resource", config.Resource,
		"password", maskPassword(config.Password),
		"test_room", config.TestRoom,
		"test_muc", config.TestMUC,
		"test_direct_message", config.TestDirectMessage,
		"test_room_exists", config.TestRoomExists,
		"test_xep0077", config.TestXEP0077)

	// Test the XMPP client
	if err := testXMPPClient(config, mainLogger); err != nil {
		mainLogger.LogError("XMPP client test failed", "error", err)
		os.Exit(1)
	}

	mainLogger.LogInfo("XMPP client test completed successfully",
		"xep0077_test", config.TestXEP0077,
		"muc_test", config.TestMUC,
		"direct_message_test", config.TestDirectMessage,
		"room_exists_test", config.TestRoomExists)
}

func testXMPPClient(config *Config, logger *StructuredLogger) error {
	logger.LogDebug("Creating XMPP client")

	// Create a structured logger for the XMPP client (reuse the passed logger)
	doctorLogger := logger

	// Create XMPP client with optional TLS configuration
	var client *xmpp.Client
	if config.InsecureSkipVerify {
		logger.LogDebug("Using insecure TLS configuration", "skip_verify", true)
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // This is a testing tool for development environments
		}
		client = xmpp.NewClientWithTLS(
			config.Server,
			config.Username,
			config.Password,
			config.Resource,
			"doctor-remote-id",
			tlsConfig,
			doctorLogger,
		)
	} else {
		client = xmpp.NewClient(
			config.Server,
			config.Username,
			config.Password,
			config.Resource,
			"doctor-remote-id",
			doctorLogger,
		)
	}

	logger.LogDebug("Attempting to connect to XMPP server", "server", config.Server)

	// Test connection
	start := time.Now()
	err := client.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}
	connectDuration := time.Since(start)

	logger.LogInfo("Connected to XMPP server", "duration", connectDuration)
	logger.LogDebug("Testing connection health")

	// Test connection health
	start = time.Now()
	err = client.Ping()
	if err != nil {
		return fmt.Errorf("connection health test failed: %w", err)
	}
	pingDuration := time.Since(start)

	logger.LogInfo("Connection health test passed", "duration", pingDuration)

	var xep0077Duration time.Duration
	var mucDuration time.Duration
	var dmDuration time.Duration
	var roomExistsDuration time.Duration

	// Test XEP-0077 In-Band Registration if requested
	if config.TestXEP0077 {
		start = time.Now()
		err = testXEP0077(client, config, logger)
		if err != nil {
			return fmt.Errorf("XEP-0077 In-Band Registration test failed: %w", err)
		}
		xep0077Duration = time.Since(start)
	}

	// Test MUC operations if requested
	if config.TestMUC {
		start = time.Now()
		err = testMUCOperations(client, config, logger)
		if err != nil {
			return fmt.Errorf("MUC operations test failed: %w", err)
		}
		mucDuration = time.Since(start)
	}

	// Test direct message if requested
	if config.TestDirectMessage {
		start = time.Now()
		err = testDirectMessage(client, config, logger)
		if err != nil {
			return fmt.Errorf("direct message test failed: %w", err)
		}
		dmDuration = time.Since(start)
	}

	// Test room existence if requested
	if config.TestRoomExists {
		start = time.Now()
		err = testRoomExists(client, config, logger)
		if err != nil {
			return fmt.Errorf("room existence test failed: %w", err)
		}
		roomExistsDuration = time.Since(start)
	}

	logger.LogDebug("Disconnecting from XMPP server")

	// Disconnect
	start = time.Now()
	err = client.Disconnect()
	if err != nil {
		return fmt.Errorf("failed to disconnect from XMPP server: %w", err)
	}
	disconnectDuration := time.Since(start)

	totalTime := connectDuration + pingDuration + disconnectDuration
	if config.TestXEP0077 {
		totalTime += xep0077Duration
	}
	if config.TestMUC {
		totalTime += mucDuration
	}
	if config.TestDirectMessage {
		totalTime += dmDuration
	}
	if config.TestRoomExists {
		totalTime += roomExistsDuration
	}

	logger.LogInfo("Disconnected from XMPP server", "disconnect_duration", disconnectDuration)
	logger.LogInfo("Connection summary",
		"connect_time", connectDuration,
		"ping_time", pingDuration,
		"xep0077_time", xep0077Duration,
		"muc_time", mucDuration,
		"direct_message_time", dmDuration,
		"room_exists_time", roomExistsDuration,
		"disconnect_time", disconnectDuration,
		"total_time", totalTime)

	return nil
}

func testMUCOperations(client *xmpp.Client, config *Config, logger *StructuredLogger) error {
	logger.LogInfo("Testing MUC operations", "room", config.TestRoom)
	logger.LogDebug("Checking if room exists first")

	// Check if room exists before attempting to join
	start := time.Now()
	exists, err := client.CheckRoomExists(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to check room existence for %s: %w", config.TestRoom, err)
	}
	checkDuration := time.Since(start)

	logger.LogInfo("Room existence check completed", "duration", checkDuration, "room", config.TestRoom, "exists", exists)

	if !exists {
		return fmt.Errorf("cannot test MUC operations: room %s does not exist or is not accessible", config.TestRoom)
	}

	logger.LogDebug("Room exists, proceeding to join")

	// Test joining the room
	start = time.Now()
	err = client.JoinRoom(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to join MUC room %s: %w", config.TestRoom, err)
	}
	joinDuration := time.Since(start)

	var sendDuration time.Duration

	logger.LogInfo("Successfully joined MUC room", "duration", joinDuration)
	logger.LogDebug("Sending test message to room")

	// Send a test message
	testMessage := fmt.Sprintf("Test message from XMPP doctor at %s", time.Now().Format("15:04:05"))
	messageReq := xmpp.MessageRequest{
		RoomJID: config.TestRoom,
		Message: testMessage,
	}

	start = time.Now()
	_, err = client.SendMessage(&messageReq)
	if err != nil {
		return fmt.Errorf("failed to send test message to room %s: %w", config.TestRoom, err)
	}
	sendDuration = time.Since(start)

	logger.LogInfo("Successfully sent message", "duration", sendDuration, "message", testMessage)
	logger.LogDebug("Waiting 5 seconds in the room")

	// Wait 5 seconds
	time.Sleep(5 * time.Second)

	logger.LogDebug("Attempting to leave MUC room")

	// Test leaving the room
	start = time.Now()
	err = client.LeaveRoom(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to leave MUC room %s: %w", config.TestRoom, err)
	}
	leaveDuration := time.Since(start)

	logger.LogInfo("Successfully left MUC room", "duration", leaveDuration)
	logger.LogInfo("MUC operations summary",
		"room_check_time", checkDuration,
		"join_time", joinDuration,
		"send_time", sendDuration,
		"wait_time", "5s",
		"leave_time", leaveDuration,
		"total_time", checkDuration+joinDuration+sendDuration+5*time.Second+leaveDuration)

	return nil
}

func testDirectMessage(client *xmpp.Client, config *Config, logger *StructuredLogger) error {
	logger.LogInfo("Testing direct message functionality")
	logger.LogDebug("Sending test message to admin user")

	// Send a test message to the admin user
	testMessage := fmt.Sprintf("Test direct message from XMPP doctor at %s", time.Now().Format("15:04:05"))
	adminJID := "admin@localhost" // Default admin user for development server

	start := time.Now()
	err := client.SendDirectMessage(adminJID, testMessage)
	if err != nil {
		return fmt.Errorf("failed to send direct message to %s: %w", adminJID, err)
	}
	sendDuration := time.Since(start)

	logger.LogInfo("Successfully sent direct message",
		"duration", sendDuration,
		"message", testMessage,
		"recipient", adminJID)
	logger.LogInfo("Direct message test summary", "send_time", sendDuration)

	return nil
}

func testRoomExists(client *xmpp.Client, config *Config, logger *StructuredLogger) error {
	logger.LogInfo("Testing room existence functionality")
	logger.LogDebug("Checking if test room exists", "room", config.TestRoom)

	// Test room existence check
	start := time.Now()
	exists, err := client.CheckRoomExists(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to check room existence for %s: %w", config.TestRoom, err)
	}
	checkDuration := time.Since(start)

	logger.LogInfo("Room existence check completed", "duration", checkDuration, "room", config.TestRoom, "exists", exists)

	// Test with a non-existent room to verify negative case
	nonExistentRoom := "nonexistent-room-12345@conference.localhost"
	logger.LogDebug("Testing negative case with non-existent room", "room", nonExistentRoom)

	start = time.Now()
	existsNegative, err := client.CheckRoomExists(nonExistentRoom)
	if err != nil {
		return fmt.Errorf("failed to check non-existent room %s: %w", nonExistentRoom, err)
	}
	checkNegativeDuration := time.Since(start)

	logger.LogInfo("Negative room existence check completed",
		"duration", checkNegativeDuration,
		"room", nonExistentRoom,
		"exists", existsNegative,
		"expected_false", true)
	logger.LogInfo("Room existence test summary",
		"test_room_time", checkDuration,
		"negative_case_time", checkNegativeDuration,
		"total_time", checkDuration+checkNegativeDuration)

	return nil
}

func maskPassword(password string) string {
	if len(password) <= 2 {
		return "****"
	}
	return password[:2] + "****"
}

// StructuredLogger provides structured logging functionality for the doctor command using slog
type StructuredLogger struct {
	logger  *slog.Logger
	verbose bool
}

// NewStructuredLogger creates a new structured logger with colorized output
func NewStructuredLogger(verbose bool) *StructuredLogger {
	// Configure log level based on verbose flag
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	// Create tinted handler for colorized output
	handler := tint.NewHandler(os.Stdout, &tint.Options{
		Level:      level,
		TimeFormat: "15:04:05.000", // More concise time format
		AddSource:  false,          // Don't show source file info
	})

	// Create logger with the handler
	logger := slog.New(handler)

	return &StructuredLogger{
		logger:  logger,
		verbose: verbose,
	}
}

// LogDebug logs debug messages with structured key-value pairs
func (l *StructuredLogger) LogDebug(msg string, keyValuePairs ...any) {
	l.logger.Debug(msg, keyValuePairs...)
}

// LogInfo logs info messages with structured key-value pairs
func (l *StructuredLogger) LogInfo(msg string, keyValuePairs ...any) {
	l.logger.Info(msg, keyValuePairs...)
}

// LogWarn logs warning messages with structured key-value pairs
func (l *StructuredLogger) LogWarn(msg string, keyValuePairs ...any) {
	l.logger.Warn(msg, keyValuePairs...)
}

// LogError logs error messages with structured key-value pairs
func (l *StructuredLogger) LogError(msg string, keyValuePairs ...any) {
	l.logger.Error(msg, keyValuePairs...)
}

// testXEP0077 tests XEP-0077 In-Band Registration by creating a ghost user and testing message sending
func testXEP0077(client *xmpp.Client, config *Config, logger *StructuredLogger) error {
	logger.LogInfo("Testing XEP-0077 In-Band Registration with ghost user messaging")

	inBandReg, err := client.GetInBandRegistration()
	if err != nil {
		return fmt.Errorf("server does not support XEP-0077 In-Band Registration: %w", err)
	}

	if !inBandReg.IsEnabled() {
		return fmt.Errorf("XEP-0077 In-Band Registration is not enabled on this server")
	}

	serverJID := client.GetJID().Domain()

	// Step 1: Create ghost user with admin client
	ghostUsername := fmt.Sprintf("ghost_test_%d", time.Now().Unix())
	ghostPassword := "testpass123"
	ghostJID := fmt.Sprintf("%s@%s", ghostUsername, serverJID.String())

	logger.LogInfo("Creating ghost user", "username", ghostUsername)

	ghostRegistrationRequest := &xmpp.RegistrationRequest{
		Username: ghostUsername,
		Password: ghostPassword,
		Email:    fmt.Sprintf("%s@localhost", ghostUsername),
	}

	ghostRegResponse, err := inBandReg.RegisterAccount(serverJID, ghostRegistrationRequest)
	if err != nil {
		return fmt.Errorf("failed to register ghost user: %w", err)
	}
	if !ghostRegResponse.Success {
		return fmt.Errorf("ghost user registration failed: %s", ghostRegResponse.Error)
	}

	logger.LogInfo("Ghost user created successfully", "username", ghostUsername)

	// Step 2-7: Use ghost client for all operations
	var ghostClient *xmpp.Client
	if config.InsecureSkipVerify {
		tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Testing tool
		ghostClient = xmpp.NewClientWithTLS(config.Server, ghostJID, ghostPassword, "ghost_doctor", "ghost-remote-id", tlsConfig, logger)
	} else {
		ghostClient = xmpp.NewClient(config.Server, ghostJID, ghostPassword, "ghost_doctor", "ghost-remote-id", logger)
	}

	// Step 2: Connect ghost client
	if connErr := ghostClient.Connect(); connErr != nil {
		return fmt.Errorf("failed to connect ghost user: %w", connErr)
	}
	logger.LogInfo("Ghost user connected")

	// Step 3: Check test room exists
	exists, err := ghostClient.CheckRoomExists(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to check room existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("test room %s does not exist", config.TestRoom)
	}

	// Step 4: Join test room
	if joinErr := ghostClient.JoinRoom(config.TestRoom); joinErr != nil {
		return fmt.Errorf("failed to join room: %w", joinErr)
	}
	logger.LogInfo("Ghost user joined room", "room", config.TestRoom)

	// Step 5: Send message to test room
	testMessage := fmt.Sprintf("Test ghost user message from %s at %s", ghostUsername, time.Now().Format("15:04:05"))
	messageReq := xmpp.MessageRequest{
		RoomJID:      config.TestRoom,
		GhostUserJID: ghostJID,
		Message:      testMessage,
	}

	if _, sendErr := ghostClient.SendMessage(&messageReq); sendErr != nil {
		return fmt.Errorf("failed to send message: %w", sendErr)
	}
	logger.LogInfo("Ghost user sent message", "message", testMessage)

	// Step 6: Cancel account (ghost user cancels their own registration)
	ghostInBandReg, err := ghostClient.GetInBandRegistration()
	if err != nil {
		return fmt.Errorf("failed to get XEP-0077 handler for ghost user: %w", err)
	}

	ghostCancellationRequest := &xmpp.CancellationRequest{Username: ghostUsername}
	ghostCancelResponse, err := ghostInBandReg.CancelRegistration(serverJID, ghostCancellationRequest)
	if err != nil {
		return fmt.Errorf("failed to cancel ghost user registration: %w", err)
	}
	if !ghostCancelResponse.Success {
		return fmt.Errorf("ghost user registration cancellation failed: %s", ghostCancelResponse.Error)
	}
	logger.LogInfo("Ghost user registration cancelled successfully")

	// Clean disconnect
	if err := ghostClient.Disconnect(); err != nil {
		logger.LogWarn("Failed to disconnect ghost client", "error", err)
	}

	logger.LogInfo("XEP-0077 ghost user test completed successfully", "username", ghostUsername)
	return nil
}
