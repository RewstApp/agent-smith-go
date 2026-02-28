package shared

import (
	"errors"
	"net"
	"net/rpc"
	"testing"

	"github.com/hashicorp/go-plugin"
)

// mockNotifier implements the Notifier interface for testing
type mockNotifier struct {
	notifyCalled bool
	notifyMsg    string
	notifyErr    error
}

func (m *mockNotifier) Notify(message string) error {
	m.notifyCalled = true
	m.notifyMsg = message
	return m.notifyErr
}

// TestNotifier_InterfaceCompliance verifies Notifier interface implementation
func TestNotifier_InterfaceCompliance(t *testing.T) {
	var _ Notifier = (*mockNotifier)(nil)
}

// TestNotifyArgs tests the NotifyArgs struct
func TestNotifyArgs(t *testing.T) {
	args := NotifyArgs{
		Message: "test message",
	}

	if args.Message != "test message" {
		t.Errorf("expected Message to be 'test message', got %q", args.Message)
	}
}

// TestEmptyReply tests the EmptyReply struct
func TestEmptyReply(t *testing.T) {
	reply := EmptyReply{}
	_ = reply // Verify it can be instantiated
}

// TestNotifierPlugin_Server tests the Server method
func TestNotifierPlugin_Server(t *testing.T) {
	mockImpl := &mockNotifier{}
	plugin := &NotifierPlugin{Impl: mockImpl}

	server, err := plugin.Server(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	rpcServer, ok := server.(*NotifierRPCServer)
	if !ok {
		t.Fatalf("expected *NotifierRPCServer, got %T", server)
	}

	if rpcServer.Impl != mockImpl {
		t.Error("expected Server to return RPC server with correct implementation")
	}
}

// TestNotifierPlugin_Client tests the Client method
func TestNotifierPlugin_Client(t *testing.T) {
	// Create a mock RPC client (nil is acceptable for this test)
	// In real usage, this would be a proper RPC client
	plugin := &NotifierPlugin{}

	client, err := plugin.Client(nil, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	rpcClient, ok := client.(*NotifierRPC)
	if !ok {
		t.Fatalf("expected *NotifierRPC, got %T", client)
	}

	if rpcClient.client != nil {
		t.Error("expected client to be nil when passed nil")
	}
}

// TestNotifierRPCServer_Notify_Success tests successful notification
func TestNotifierRPCServer_Notify_Success(t *testing.T) {
	mockImpl := &mockNotifier{}
	server := &NotifierRPCServer{Impl: mockImpl}

	args := NotifyArgs{Message: "test notification"}
	reply := &EmptyReply{}

	err := server.Notify(args, reply)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !mockImpl.notifyCalled {
		t.Error("expected Notify to be called on implementation")
	}

	if mockImpl.notifyMsg != "test notification" {
		t.Errorf("expected message 'test notification', got %q", mockImpl.notifyMsg)
	}
}

// TestNotifierRPCServer_Notify_Error tests notification with error
func TestNotifierRPCServer_Notify_Error(t *testing.T) {
	expectedErr := errors.New("notification failed")
	mockImpl := &mockNotifier{notifyErr: expectedErr}
	server := &NotifierRPCServer{Impl: mockImpl}

	args := NotifyArgs{Message: "test"}
	reply := &EmptyReply{}

	err := server.Notify(args, reply)
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	if !mockImpl.notifyCalled {
		t.Error("expected Notify to be called even on error")
	}
}

// TestNotifierRPCServer_Notify_EmptyMessage tests notification with empty message
func TestNotifierRPCServer_Notify_EmptyMessage(t *testing.T) {
	mockImpl := &mockNotifier{}
	server := &NotifierRPCServer{Impl: mockImpl}

	args := NotifyArgs{Message: ""}
	reply := &EmptyReply{}

	err := server.Notify(args, reply)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if mockImpl.notifyMsg != "" {
		t.Errorf("expected empty message, got %q", mockImpl.notifyMsg)
	}
}

// TestNotifierRPC_Notify_Integration tests NotifierRPC with a real RPC connection
func TestNotifierRPC_Notify_Integration(t *testing.T) {
	// Create a mock implementation
	mockImpl := &mockNotifier{}

	// Create RPC server
	server := &NotifierRPCServer{Impl: mockImpl}

	// Register the server with RPC
	rpcServer := rpc.NewServer()
	err := rpcServer.RegisterName("Plugin", server)
	if err != nil {
		t.Fatalf("failed to register RPC server: %v", err)
	}

	// Create a pipe for communication
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Start server in goroutine
	go rpcServer.ServeConn(serverConn)

	// Create RPC client
	rpcClient := rpc.NewClient(clientConn)
	defer rpcClient.Close()

	// Create NotifierRPC with the client
	notifier := &NotifierRPC{client: rpcClient}

	// Test successful notification
	err = notifier.Notify("integration test message")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the message was received
	if !mockImpl.notifyCalled {
		t.Error("expected Notify to be called on implementation")
	}

	if mockImpl.notifyMsg != "integration test message" {
		t.Errorf("expected message 'integration test message', got %q", mockImpl.notifyMsg)
	}
}

// TestNotifierRPC_Notify_Integration_Error tests NotifierRPC with error response
func TestNotifierRPC_Notify_Integration_Error(t *testing.T) {
	// Create a mock implementation that returns an error
	expectedErr := errors.New("mock notification error")
	mockImpl := &mockNotifier{notifyErr: expectedErr}

	// Create RPC server
	server := &NotifierRPCServer{Impl: mockImpl}

	// Register the server with RPC
	rpcServer := rpc.NewServer()
	err := rpcServer.RegisterName("Plugin", server)
	if err != nil {
		t.Fatalf("failed to register RPC server: %v", err)
	}

	// Create a pipe for communication
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Start server in goroutine
	go rpcServer.ServeConn(serverConn)

	// Create RPC client
	rpcClient := rpc.NewClient(clientConn)
	defer rpcClient.Close()

	// Create NotifierRPC with the client
	notifier := &NotifierRPC{client: rpcClient}

	// Test notification that returns an error
	err = notifier.Notify("test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != expectedErr.Error() {
		t.Errorf("expected error %q, got %q", expectedErr.Error(), err.Error())
	}
}

// TestNotifierRPC_Structure tests NotifierRPC structure
func TestNotifierRPC_Structure(t *testing.T) {
	notifier := &NotifierRPC{client: nil}

	// Verify the struct can be created
	if notifier == nil {
		t.Error("expected non-nil NotifierRPC")
	}

	// Verify it implements Notifier interface
	var _ Notifier = notifier
}

// TestNotifierPlugin_ImplementsPluginInterface verifies NotifierPlugin implements plugin.Plugin
func TestNotifierPlugin_ImplementsPluginInterface(t *testing.T) {
	var _ plugin.Plugin = (*NotifierPlugin)(nil)
}

// TestNotifierPlugin_WithNilImpl tests plugin with nil implementation
func TestNotifierPlugin_WithNilImpl(t *testing.T) {
	plugin := &NotifierPlugin{Impl: nil}

	server, err := plugin.Server(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	rpcServer, ok := server.(*NotifierRPCServer)
	if !ok {
		t.Fatalf("expected *NotifierRPCServer, got %T", server)
	}

	if rpcServer.Impl != nil {
		t.Error("expected nil implementation")
	}
}

// TestNotifierRPCServer_Notify_WithNilImpl tests RPC server with nil implementation
func TestNotifierRPCServer_Notify_WithNilImpl(t *testing.T) {
	server := &NotifierRPCServer{Impl: nil}

	args := NotifyArgs{Message: "test"}
	reply := &EmptyReply{}

	// This should panic - testing the behavior
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when Impl is nil")
		}
	}()

	server.Notify(args, reply)
}

// TestNotifyArgs_MultipleMessages tests creating multiple NotifyArgs
func TestNotifyArgs_MultipleMessages(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"simple", "hello"},
		{"with_spaces", "hello world"},
		{"with_newlines", "hello\nworld"},
		{"unicode", "hello 世界"},
		{"long", "this is a very long message that contains a lot of text to test how the system handles longer messages"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := NotifyArgs{Message: tt.message}
			if args.Message != tt.message {
				t.Errorf("expected message %q, got %q", tt.message, args.Message)
			}
		})
	}
}
