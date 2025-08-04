package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

const (
	// Default values for development server (sidecar)
	defaultServer   = "localhost:5222"
	defaultUsername = "testuser@localhost"
	defaultPassword = "testpass"
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
	flag.BoolVar(&config.Verbose, "verbose", true, "Enable verbose logging")
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

	if config.Verbose {
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
		log.Printf("Starting XMPP client doctor...")
		log.Printf("Configuration:")
		log.Printf("  Server: %s", config.Server)
		log.Printf("  Username: %s", config.Username)
		log.Printf("  Resource: %s", config.Resource)
		log.Printf("  Password: %s", maskPassword(config.Password))
		if config.TestMUC {
			log.Printf("  Test Room: %s", config.TestRoom)
		}
		if config.TestDirectMessage {
			log.Printf("  Test Direct Messages: enabled")
		}
		if config.TestRoomExists {
			log.Printf("  Test Room Existence: enabled")
		}
	}

	// Test the XMPP client
	if err := testXMPPClient(config); err != nil {
		log.Fatalf("❌ XMPP client test failed: %v", err)
	}

	if config.Verbose {
		log.Printf("✅ XMPP client test completed successfully!")
	} else {
		fmt.Println("✅ XMPP client connectivity test passed!")
		if config.TestMUC {
			fmt.Println("✅ XMPP MUC operations test passed!")
		}
		if config.TestDirectMessage {
			fmt.Println("✅ XMPP direct message test passed!")
		}
		if config.TestRoomExists {
			fmt.Println("✅ XMPP room existence test passed!")
		}
	}
}

func testXMPPClient(config *Config) error {
	if config.Verbose {
		log.Printf("Creating XMPP client...")
	}

	// Create a simple logger for the XMPP client
	doctorLogger := &SimpleLogger{verbose: config.Verbose}

	// Create XMPP client with optional TLS configuration
	var client *xmpp.Client
	if config.InsecureSkipVerify {
		if config.Verbose {
			log.Printf("Using insecure TLS configuration (skipping certificate verification)")
		}
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
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

	if config.Verbose {
		log.Printf("Attempting to connect to XMPP server...")
	}

	// Test connection
	start := time.Now()
	err := client.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}
	connectDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Connected to XMPP server in %v", connectDuration)
		log.Printf("Testing connection health...")
	}

	// Test connection health
	start = time.Now()
	err = client.Ping()
	if err != nil {
		return fmt.Errorf("connection health test failed: %w", err)
	}
	pingDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Connection health test passed in %v", pingDuration)
	}

	var mucDuration time.Duration
	var dmDuration time.Duration
	var roomExistsDuration time.Duration
	
	// Test MUC operations if requested
	if config.TestMUC {
		start = time.Now()
		err = testMUCOperations(client, config)
		if err != nil {
			return fmt.Errorf("MUC operations test failed: %w", err)
		}
		mucDuration = time.Since(start)
	}

	// Test direct message if requested
	if config.TestDirectMessage {
		start = time.Now()
		err = testDirectMessage(client, config)
		if err != nil {
			return fmt.Errorf("direct message test failed: %w", err)
		}
		dmDuration = time.Since(start)
	}

	// Test room existence if requested
	if config.TestRoomExists {
		start = time.Now()
		err = testRoomExists(client, config)
		if err != nil {
			return fmt.Errorf("room existence test failed: %w", err)
		}
		roomExistsDuration = time.Since(start)
	}

	if config.Verbose {
		log.Printf("Disconnecting from XMPP server...")
	}

	// Disconnect
	start = time.Now()
	err = client.Disconnect()
	if err != nil {
		return fmt.Errorf("failed to disconnect from XMPP server: %w", err)
	}
	disconnectDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Disconnected from XMPP server in %v", disconnectDuration)
		log.Printf("Connection summary:")
		log.Printf("  Connect time: %v", connectDuration)
		log.Printf("  Ping time: %v", pingDuration)
		if config.TestMUC {
			log.Printf("  MUC operations time: %v", mucDuration)
		}
		if config.TestDirectMessage {
			log.Printf("  Direct message time: %v", dmDuration)
		}
		if config.TestRoomExists {
			log.Printf("  Room existence check time: %v", roomExistsDuration)
		}
		log.Printf("  Disconnect time: %v", disconnectDuration)
		totalTime := connectDuration + pingDuration + disconnectDuration
		if config.TestMUC {
			totalTime += mucDuration
		}
		if config.TestDirectMessage {
			totalTime += dmDuration
		}
		if config.TestRoomExists {
			totalTime += roomExistsDuration
		}
		log.Printf("  Total time: %v", totalTime)
	}

	return nil
}

func testMUCOperations(client *xmpp.Client, config *Config) error {
	if config.Verbose {
		log.Printf("Testing MUC operations with room: %s", config.TestRoom)
		log.Printf("First checking if room exists...")
	}

	// Check if room exists before attempting to join
	start := time.Now()
	exists, err := client.CheckRoomExists(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to check room existence for %s: %w", config.TestRoom, err)
	}
	checkDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Room existence check completed in %v", checkDuration)
		log.Printf("Room %s exists: %t", config.TestRoom, exists)
	}

	if !exists {
		return fmt.Errorf("cannot test MUC operations: room %s does not exist or is not accessible", config.TestRoom)
	}

	if config.Verbose {
		log.Printf("Room exists, proceeding to join...")
	}

	// Test joining the room
	start = time.Now()
	err = client.JoinRoom(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to join MUC room %s: %w", config.TestRoom, err)
	}
	joinDuration := time.Since(start)
	
	var sendDuration time.Duration

	if config.Verbose {
		log.Printf("✅ Successfully joined MUC room in %v", joinDuration)
		log.Printf("Sending test message to room...")
	}

	// Send a test message
	testMessage := fmt.Sprintf("Test message from XMPP doctor at %s", time.Now().Format("15:04:05"))
	messageReq := xmpp.MessageRequest{
		RoomJID: config.TestRoom,
		Message: testMessage,
	}
	
	start = time.Now()
	_, err = client.SendMessage(messageReq)
	if err != nil {
		return fmt.Errorf("failed to send test message to room %s: %w", config.TestRoom, err)
	}
	sendDuration = time.Since(start)

	if config.Verbose {
		log.Printf("✅ Successfully sent message in %v", sendDuration)
		log.Printf("Message: %s", testMessage)
		log.Printf("Waiting 5 seconds in the room...")
	}

	// Wait 5 seconds
	time.Sleep(5 * time.Second)

	if config.Verbose {
		log.Printf("Attempting to leave MUC room...")
	}

	// Test leaving the room
	start = time.Now()
	err = client.LeaveRoom(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to leave MUC room %s: %w", config.TestRoom, err)
	}
	leaveDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Successfully left MUC room in %v", leaveDuration)
		log.Printf("MUC operations summary:")
		log.Printf("  Room existence check time: %v", checkDuration)
		log.Printf("  Join time: %v", joinDuration)
		log.Printf("  Send message time: %v", sendDuration)
		log.Printf("  Wait time: 5s")
		log.Printf("  Leave time: %v", leaveDuration)
		log.Printf("  Total MUC time: %v", checkDuration+joinDuration+sendDuration+5*time.Second+leaveDuration)
	}

	return nil
}

func testDirectMessage(client *xmpp.Client, config *Config) error {
	if config.Verbose {
		log.Printf("Testing direct message functionality...")
		log.Printf("Sending test message to admin user...")
	}

	// Send a test message to the admin user
	testMessage := fmt.Sprintf("Test direct message from XMPP doctor at %s", time.Now().Format("15:04:05"))
	adminJID := "admin@localhost" // Default admin user for development server
	
	start := time.Now()
	err := client.SendDirectMessage(adminJID, testMessage)
	if err != nil {
		return fmt.Errorf("failed to send direct message to %s: %w", adminJID, err)
	}
	sendDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Successfully sent direct message in %v", sendDuration)
		log.Printf("Message: %s", testMessage)
		log.Printf("Recipient: %s", adminJID)
		log.Printf("Direct message test summary:")
		log.Printf("  Send message time: %v", sendDuration)
	}

	return nil
}

func testRoomExists(client *xmpp.Client, config *Config) error {
	if config.Verbose {
		log.Printf("Testing room existence functionality...")
		log.Printf("Checking if test room exists: %s", config.TestRoom)
	}

	// Test room existence check
	start := time.Now()
	exists, err := client.CheckRoomExists(config.TestRoom)
	if err != nil {
		return fmt.Errorf("failed to check room existence for %s: %w", config.TestRoom, err)
	}
	checkDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Room existence check completed in %v", checkDuration)
		log.Printf("Room %s exists: %t", config.TestRoom, exists)
	}

	// Test with a non-existent room to verify negative case
	nonExistentRoom := "nonexistent-room-12345@conference.localhost"
	if config.Verbose {
		log.Printf("Testing negative case with non-existent room: %s", nonExistentRoom)
	}

	start = time.Now()
	existsNegative, err := client.CheckRoomExists(nonExistentRoom)
	if err != nil {
		return fmt.Errorf("failed to check non-existent room %s: %w", nonExistentRoom, err)
	}
	checkNegativeDuration := time.Since(start)

	if config.Verbose {
		log.Printf("✅ Negative room existence check completed in %v", checkNegativeDuration)
		log.Printf("Non-existent room %s exists: %t (should be false)", nonExistentRoom, existsNegative)
		log.Printf("Room existence test summary:")
		log.Printf("  Test room check time: %v", checkDuration)
		log.Printf("  Negative case check time: %v", checkNegativeDuration)
		log.Printf("  Total room existence test time: %v", checkDuration+checkNegativeDuration)
	}

	return nil
}

func maskPassword(password string) string {
	if len(password) <= 2 {
		return "****"
	}
	return password[:2] + "****"
}

// SimpleLogger provides basic logging functionality for the doctor command
type SimpleLogger struct {
	verbose bool
}

// LogDebug logs debug messages if verbose mode is enabled
func (l *SimpleLogger) LogDebug(msg string, args ...interface{}) {
	if l.verbose {
		log.Printf("[DEBUG] "+msg, args...)
	}
}

// LogInfo logs info messages
func (l *SimpleLogger) LogInfo(msg string, args ...interface{}) {
	log.Printf("[INFO] "+msg, args...)
}

// LogWarn logs warning messages
func (l *SimpleLogger) LogWarn(msg string, args ...interface{}) {
	log.Printf("[WARN] "+msg, args...)
}

// LogError logs error messages
func (l *SimpleLogger) LogError(msg string, args ...interface{}) {
	log.Printf("[ERROR] "+msg, args...)
}