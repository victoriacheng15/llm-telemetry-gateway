package gateway

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDialAndSend(t *testing.T) {
	// Create a temporary UDS socket path for testing
	tempDir := t.TempDir()
	testSocket := filepath.Join(tempDir, "test_policy.sock")

	// Set up a mock UDS server listener
	listener, err := net.Listen("unix", testSocket)
	if err != nil {
		t.Fatalf("Failed to create mock UDS listener: %v", err)
	}
	defer listener.Close()

	// Mock server response handler running in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				// Read payload from socket
				buf := make([]byte, 1024)
				n, err := c.Read(buf)
				if err != nil {
					return
				}

				payload := string(buf[:n])
				var response string
				// Return redacted payload if matching mock SSN client request
				if payload == `{"prompt": "Diagnostic review: Client SSN is 123-45-6789"}`+"\n" {
					response = `{"prompt": "Diagnostic review: Client SSN is [REDACTED_SSN]"}` + "\n"
				} else {
					response = payload
				}

				c.Write([]byte(response))
			}(conn)
		}
	}()

	tests := []struct {
		name     string
		path     string
		payload  string
		expected string
		wantErr  bool
	}{
		{
			name:     "successful UDS dial and redact",
			path:     testSocket,
			payload:  `{"prompt": "Diagnostic review: Client SSN is 123-45-6789"}` + "\n",
			expected: `{"prompt": "Diagnostic review: Client SSN is [REDACTED_SSN]"}` + "\n",
			wantErr:  false,
		},
		{
			name:     "failed dial due to missing socket path",
			path:     filepath.Join(tempDir, "non_existent.sock"),
			payload:  "some payload\n",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DialAndSend(tt.path, tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("DialAndSend() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("DialAndSend() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHandleCompletions(t *testing.T) {
	// Create a temporary UDS socket path for testing
	tempDir := t.TempDir()
	testSocket := filepath.Join(tempDir, "test_policy.sock")

	// Set up a mock UDS server listener
	listener, err := net.Listen("unix", testSocket)
	if err != nil {
		t.Fatalf("Failed to create mock UDS listener: %v", err)
	}
	defer listener.Close()

	// Mock server response handler running in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				payload := string(buf[:n])
				var response string
				if strings.Contains(payload, "123-45-6789") {
					response = `{"prompt": "Client SSN is [REDACTED_SSN]"}` + "\n"
				} else {
					response = payload
				}
				c.Write([]byte(response))
			}(conn)
		}
	}()

	// Temporarily override the package-level SocketPath
	oldSocketPath := SocketPath
	SocketPath = testSocket
	defer func() { SocketPath = oldSocketPath }()

	tests := []struct {
		name           string
		method         string
		payload        string
		useWrongSocket bool
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "successful completions proxying",
			method:         "POST",
			payload:        `{"prompt": "Client SSN is 123-45-6789"}`,
			expectedStatus: http.StatusOK,
			expectedBody:   `{"prompt": "Client SSN is [REDACTED_SSN]"}`,
		},
		{
			name:           "fail-closed on unreachable policy engine",
			method:         "POST",
			payload:        `{"prompt": "Client SSN is 123-45-6789"}`,
			useWrongSocket: true,
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody:   `{"error": "PII policy engine unreachable"}`,
		},
		{
			name:           "method not allowed",
			method:         "GET",
			payload:        "",
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.useWrongSocket {
				SocketPath = filepath.Join(tempDir, "non_existent.sock")
			} else {
				SocketPath = testSocket
			}

			req := httptest.NewRequest(tt.method, "/v1/chat/completions", bytes.NewBufferString(tt.payload))
			rr := httptest.NewRecorder()

			HandleCompletions(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			gotBody := strings.TrimSpace(rr.Body.String())
			wantBody := strings.TrimSpace(tt.expectedBody)
			if gotBody != wantBody {
				t.Errorf("expected body %q, got %q", wantBody, gotBody)
			}
		})
	}
}

func TestHandleHealthz(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "successful healthz request",
			method:         "GET",
			expectedStatus: http.StatusOK,
			expectedBody:   `{"status": "healthy"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/healthz", nil)
			rr := httptest.NewRecorder()

			HandleHealthz(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			gotBody := strings.TrimSpace(rr.Body.String())
			wantBody := strings.TrimSpace(tt.expectedBody)
			if gotBody != wantBody {
				t.Errorf("expected body %q, got %q", wantBody, gotBody)
			}
		})
	}
}

func TestHandleReadyz(t *testing.T) {
	tempDir := t.TempDir()
	testSocket := filepath.Join(tempDir, "test_ready.sock")

	// Set up socket listener for the ready case
	listener, err := net.Listen("unix", testSocket)
	if err != nil {
		t.Fatalf("Failed to create mock UDS listener: %v", err)
	}
	defer listener.Close()

	tests := []struct {
		name           string
		socketPath     string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "readyz success when UDS connectable",
			socketPath:     testSocket,
			expectedStatus: http.StatusOK,
			expectedBody:   `{"status": "ready"}`,
		},
		{
			name:           "readyz failure when UDS unreachable",
			socketPath:     filepath.Join(tempDir, "non_existent.sock"),
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody:   `{"status": "unready"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSocketPath := SocketPath
			SocketPath = tt.socketPath
			defer func() { SocketPath = oldSocketPath }()

			req := httptest.NewRequest("GET", "/readyz", nil)
			rr := httptest.NewRecorder()

			HandleReadyz(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			body := rr.Body.String()
			if tt.expectedStatus == http.StatusOK {
				gotBody := strings.TrimSpace(body)
				wantBody := strings.TrimSpace(tt.expectedBody)
				if gotBody != wantBody {
					t.Errorf("expected body %q, got %q", wantBody, gotBody)
				}
			} else {
				if !strings.Contains(body, tt.expectedBody) {
					t.Errorf("expected body to contain %q, got %q", tt.expectedBody, body)
				}
			}
		})
	}
}

func TestRun(t *testing.T) {
	// Execute Run in a goroutine on a random port
	go Run("127.0.0.1:0")

	// Wait briefly for server startup
	time.Sleep(100 * time.Millisecond)

	// Send a SIGINT shutdown signal directly to the exported SigChan
	SigChan <- syscall.SIGINT

	// Give the server thread time to stop gracefully
	time.Sleep(100 * time.Millisecond)
}
