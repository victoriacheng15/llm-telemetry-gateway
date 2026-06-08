package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

// Helper process for mocking execCommand
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "No command\n")
		os.Exit(2)
	}

	cmd := args[0]
	if os.Getenv("MOCK_FAIL") == "1" {
		fmt.Fprintf(os.Stderr, "mocked error: failed to run %s\n", cmd)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "mocked output for %s %s\n", cmd, strings.Join(args[1:], " "))
}

func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{
		"GO_WANT_HELPER_PROCESS=1",
		"MOCK_FAIL=" + os.Getenv("MOCK_FAIL"),
	}
	return cmd
}

func TestHandleChaosStress(t *testing.T) {
	oldExec := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = oldExec }()

	tests := []struct {
		name           string
		method         string
		mockFail       bool
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "POST success",
			method:         http.MethodPost,
			expectedStatus: http.StatusOK,
			expectedBody:   `mocked output for kubectl apply -f /app/k3s/chaos-mesh/node-stress.yaml`,
		},
		{
			name:           "DELETE success",
			method:         http.MethodDelete,
			expectedStatus: http.StatusOK,
			expectedBody:   `mocked output for kubectl delete -f /app/k3s/chaos-mesh/node-stress.yaml`,
		},
		{
			name:           "OPTIONS success",
			method:         http.MethodOptions,
			expectedStatus: http.StatusOK,
			expectedBody:   "",
		},
		{
			name:           "Method Not Allowed",
			method:         http.MethodGet,
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "",
		},
		{
			name:           "Command failure",
			method:         http.MethodPost,
			mockFail:       true,
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `error`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFail {
				t.Setenv("MOCK_FAIL", "1")
			} else {
				t.Setenv("MOCK_FAIL", "0")
			}

			req := httptest.NewRequest(tt.method, "/api/chaos/stress", nil)
			rr := httptest.NewRecorder()

			HandleChaosStress(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}
			if tt.expectedBody != "" && !strings.Contains(rr.Body.String(), tt.expectedBody) {
				t.Errorf("expected body to contain %q, got %q", tt.expectedBody, rr.Body.String())
			}
		})
	}
}

func TestHandleChaosNetwork(t *testing.T) {
	oldExec := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = oldExec }()

	tests := []struct {
		name           string
		method         string
		mockFail       bool
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "POST success",
			method:         http.MethodPost,
			expectedStatus: http.StatusOK,
			expectedBody:   `mocked output for kubectl apply -f /app/k3s/chaos-mesh/network-delay.yaml`,
		},
		{
			name:           "DELETE success",
			method:         http.MethodDelete,
			expectedStatus: http.StatusOK,
			expectedBody:   `mocked output for kubectl delete -f /app/k3s/chaos-mesh/network-delay.yaml`,
		},
		{
			name:           "OPTIONS success",
			method:         http.MethodOptions,
			expectedStatus: http.StatusOK,
			expectedBody:   "",
		},
		{
			name:           "Method Not Allowed",
			method:         http.MethodGet,
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "",
		},
		{
			name:           "Command failure",
			method:         http.MethodPost,
			mockFail:       true,
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `error`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFail {
				t.Setenv("MOCK_FAIL", "1")
			} else {
				t.Setenv("MOCK_FAIL", "0")
			}

			req := httptest.NewRequest(tt.method, "/api/chaos/network", nil)
			rr := httptest.NewRecorder()

			HandleChaosNetwork(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}
			if tt.expectedBody != "" && !strings.Contains(rr.Body.String(), tt.expectedBody) {
				t.Errorf("expected body to contain %q, got %q", tt.expectedBody, rr.Body.String())
			}
		})
	}
}

func TestHandleDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	diagFile := filepath.Join(tempDir, "diagnostics.txt")

	oldPath := DiagnosticsPath
	DiagnosticsPath = diagFile
	defer func() { DiagnosticsPath = oldPath }()

	// Test 1: File not exist
	req := httptest.NewRequest("GET", "/api/diagnostics", nil)
	rr := httptest.NewRecorder()
	HandleDiagnostics(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != "Diagnostics not yet available." {
		t.Errorf("expected body 'Diagnostics not yet available.', got %q", rr.Body.String())
	}

	// Test 2: File exists
	err := os.WriteFile(diagFile, []byte("metrics payload"), 0644)
	if err != nil {
		t.Fatalf("failed to write mock diagnostics: %v", err)
	}

	req = httptest.NewRequest("GET", "/api/diagnostics", nil)
	rr = httptest.NewRecorder()
	HandleDiagnostics(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != "metrics payload" {
		t.Errorf("expected body 'metrics payload', got %q", rr.Body.String())
	}

	// Test 3: Method Not Allowed
	req = httptest.NewRequest("POST", "/api/diagnostics", nil)
	rr = httptest.NewRecorder()
	HandleDiagnostics(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestHandleRCALogStream(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "rca.log")

	oldPath := RCALogPath
	RCALogPath = logFile
	defer func() { RCALogPath = oldPath }()

	err := os.WriteFile(logFile, []byte("line 1\nline 2\n"), 0644)
	if err != nil {
		t.Fatalf("failed to write mock log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/logs/stream", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	// Run handler in goroutine and cancel context after 50ms to exit the loop
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	HandleRCALogStream(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", contentType)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "data: line 1") || !strings.Contains(body, "data: line 2") {
		t.Errorf("expected streamed data, got %q", body)
	}
}

func TestHandleMaskTest(t *testing.T) {
	// Set up UDS Mock Server
	tempDir := t.TempDir()
	testSocket := filepath.Join(tempDir, "test_mask.sock")

	listener, err := net.Listen("unix", testSocket)
	if err != nil {
		t.Fatalf("Failed to create mock UDS listener: %v", err)
	}
	defer listener.Close()

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
					response = "My SSN is [REDACTED_SSN]\n"
				} else {
					response = payload
				}
				c.Write([]byte(response))
			}(conn)
		}
	}()

	oldSocket := SocketPath
	SocketPath = testSocket
	defer func() { SocketPath = oldSocket }()

	// Test 1: Successful Masking
	reqPayload := `{"prompt": "My SSN is 123-45-6789"}`
	req := httptest.NewRequest("POST", "/api/mask", bytes.NewBufferString(reqPayload))
	rr := httptest.NewRecorder()

	HandleMaskTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	var res MaskResponse
	err = json.Unmarshal(rr.Body.Bytes(), &res)
	if err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if res.Masked != "My SSN is [REDACTED_SSN]" {
		t.Errorf("expected masked 'My SSN is [REDACTED_SSN]', got %q", res.Masked)
	}

	// Test 2: Invalid JSON
	req = httptest.NewRequest("POST", "/api/mask", bytes.NewBufferString("invalid json"))
	rr = httptest.NewRecorder()
	HandleMaskTest(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	// Test 3: Method Not Allowed
	req = httptest.NewRequest("GET", "/api/mask", nil)
	rr = httptest.NewRecorder()
	HandleMaskTest(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}
