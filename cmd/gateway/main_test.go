package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
			got, err := dialAndSend(tt.path, tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("dialAndSend() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("dialAndSend() = %v, want %v", got, tt.expected)
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

	// Temporarily override the package-level socketPath
	oldSocketPath := socketPath
	socketPath = testSocket
	defer func() { socketPath = oldSocketPath }()

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
				socketPath = filepath.Join(tempDir, "non_existent.sock")
			} else {
				socketPath = testSocket
			}

			req := httptest.NewRequest(tt.method, "/v1/chat/completions", bytes.NewBufferString(tt.payload))
			rr := httptest.NewRecorder()

			handleCompletions(rr, req)

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
