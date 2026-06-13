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
	"runtime"
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
	tests := []struct {
		name string
		addr string
	}{
		{
			name: "run and shutdown random port",
			addr: "127.0.0.1:0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			go Run(tt.addr)
			time.Sleep(100 * time.Millisecond)
			SigChan <- syscall.SIGINT
			time.Sleep(100 * time.Millisecond)
		})
	}
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

	tests := []struct {
		name           string
		method         string
		setupFunc      func()
		expectedStatus int
		expectedBody   string
	}{
		{
			name:   "file not exist",
			method: "GET",
			setupFunc: func() {
				os.Remove(diagFile)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "Diagnostics not yet available.",
		},
		{
			name:   "file exists",
			method: "GET",
			setupFunc: func() {
				os.WriteFile(diagFile, []byte("metrics payload"), 0644)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "metrics payload",
		},
		{
			name:           "method not allowed",
			method:         "POST",
			setupFunc:      func() {},
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupFunc()
			req := httptest.NewRequest(tt.method, "/api/diagnostics", nil)
			rr := httptest.NewRecorder()
			HandleDiagnostics(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}
			if tt.expectedBody != "" && rr.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, rr.Body.String())
			}
		})
	}
}

func TestHandleRCALogStream(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "rca.log")

	oldPath := RCALogPath
	RCALogPath = logFile
	defer func() { RCALogPath = oldPath }()

	tests := []struct {
		name           string
		fileContent    string
		expectedStatus int
		expectedSubstr []string
	}{
		{
			name:           "stream log file contents",
			fileContent:    "line 1\nline 2\n",
			expectedStatus: http.StatusOK,
			expectedSubstr: []string{"data: line 1", "data: line 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := os.WriteFile(logFile, []byte(tt.fileContent), 0644)
			if err != nil {
				t.Fatalf("failed to write mock log: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest("GET", "/api/logs/stream", nil).WithContext(ctx)
			rr := httptest.NewRecorder()

			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()

			HandleRCALogStream(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			contentType := rr.Header().Get("Content-Type")
			if contentType != "text/event-stream" {
				t.Errorf("expected Content-Type text/event-stream, got %q", contentType)
			}

			body := rr.Body.String()
			for _, sub := range tt.expectedSubstr {
				if !strings.Contains(body, sub) {
					t.Errorf("expected streamed data to contain %q, got %q", sub, body)
				}
			}
		})
	}
}

func TestHandleMaskTest(t *testing.T) {
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

	tests := []struct {
		name           string
		method         string
		payload        string
		expectedStatus int
		expectedMasked string
	}{
		{
			name:           "successful masking",
			method:         "POST",
			payload:        `{"prompt": "My SSN is 123-45-6789"}`,
			expectedStatus: http.StatusOK,
			expectedMasked: "My SSN is [REDACTED_SSN]",
		},
		{
			name:           "invalid json",
			method:         "POST",
			payload:        "invalid json",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "method not allowed",
			method:         "GET",
			payload:        "",
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.payload != "" {
				req = httptest.NewRequest(tt.method, "/api/mask", bytes.NewBufferString(tt.payload))
			} else {
				req = httptest.NewRequest(tt.method, "/api/mask", nil)
			}
			rr := httptest.NewRecorder()

			HandleMaskTest(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			if tt.expectedStatus == http.StatusOK {
				var res MaskResponse
				err = json.Unmarshal(rr.Body.Bytes(), &res)
				if err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}
				if res.Masked != tt.expectedMasked {
					t.Errorf("expected masked %q, got %q", tt.expectedMasked, res.Masked)
				}
			}
		})
	}
}

func TestReadCPUUsageCgroupV2(t *testing.T) {
	tempDir := t.TempDir()
	cpuStatPath = filepath.Join(tempDir, "cpu.stat")
	defer func() { cpuStatPath = "/sys/fs/cgroup/cpu.stat" }()

	tests := []struct {
		name        string
		fileContent string
		expectedVal int64
		expectErr   bool
	}{
		{
			name:        "valid usage_usec",
			fileContent: "usage_usec 1000000\nsome_other_field 12345\n",
			expectedVal: 1000000000,
			expectErr:   false,
		},
		{
			name:        "invalid data format",
			fileContent: "usage_usec_invalid\n",
			expectedVal: 0,
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(cpuStatPath, []byte(tt.fileContent), 0644)
			val, err := readCPUUsageCgroupV2()
			if (err != nil) != tt.expectErr {
				t.Errorf("expected error = %v, got %v", tt.expectErr, err)
			}
			if val != tt.expectedVal {
				t.Errorf("expected value = %v, got %v", tt.expectedVal, val)
			}
		})
	}
}

func TestReadCPUUsageCgroupV1(t *testing.T) {
	tempDir := t.TempDir()
	cpuacctUsagePath = filepath.Join(tempDir, "cpuacct.usage")
	defer func() { cpuacctUsagePath = "/sys/fs/cgroup/cpuacct/cpuacct.usage" }()

	tests := []struct {
		name        string
		fileContent string
		expectedVal int64
		expectErr   bool
	}{
		{
			name:        "valid cpuacct usage",
			fileContent: "2000000000\n",
			expectedVal: 2000000000,
			expectErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(cpuacctUsagePath, []byte(tt.fileContent), 0644)
			val, err := readCPUUsageCgroupV1()
			if (err != nil) != tt.expectErr {
				t.Errorf("expected error = %v, got %v", tt.expectErr, err)
			}
			if val != tt.expectedVal {
				t.Errorf("expected value = %v, got %v", tt.expectedVal, val)
			}
		})
	}
}

func TestReadCPUUsageProc(t *testing.T) {
	tempDir := t.TempDir()
	procStatPath = filepath.Join(tempDir, "stat")
	defer func() { procStatPath = "/proc/self/stat" }()

	tests := []struct {
		name        string
		fileContent string
		expectedVal int64
	}{
		{
			name:        "valid proc stat",
			fileContent: "(test_proc) 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17\n",
			expectedVal: 250000000,
		},
		{
			name:        "invalid proc stat",
			fileContent: "(test_proc) 1 2\n",
			expectedVal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(procStatPath, []byte(tt.fileContent), 0644)
			val := readCPUUsageProc()
			if val != tt.expectedVal {
				t.Errorf("expected value = %v, got %v", tt.expectedVal, val)
			}
		})
	}
}

func TestReadMemoryUsageCgroupV2(t *testing.T) {
	tempDir := t.TempDir()
	memCurrentPath = filepath.Join(tempDir, "memory.current")
	defer func() { memCurrentPath = "/sys/fs/cgroup/memory.current" }()

	tests := []struct {
		name        string
		fileContent string
		expectedVal int64
		expectErr   bool
	}{
		{
			name:        "valid memory usage",
			fileContent: "104857600\n",
			expectedVal: 104857600,
			expectErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(memCurrentPath, []byte(tt.fileContent), 0644)
			val, err := readMemoryUsageCgroupV2()
			if (err != nil) != tt.expectErr {
				t.Errorf("expected error = %v, got %v", tt.expectErr, err)
			}
			if val != tt.expectedVal {
				t.Errorf("expected value = %v, got %v", tt.expectedVal, val)
			}
		})
	}
}

func TestReadMemoryUsageCgroupV1(t *testing.T) {
	tempDir := t.TempDir()
	memUsagePath = filepath.Join(tempDir, "memory.usage_in_bytes")
	defer func() { memUsagePath = "/sys/fs/cgroup/memory/memory.usage_in_bytes" }()

	tests := []struct {
		name        string
		fileContent string
		expectedVal int64
		expectErr   bool
	}{
		{
			name:        "valid memory usage in bytes",
			fileContent: "209715200\n",
			expectedVal: 209715200,
			expectErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(memUsagePath, []byte(tt.fileContent), 0644)
			val, err := readMemoryUsageCgroupV1()
			if (err != nil) != tt.expectErr {
				t.Errorf("expected error = %v, got %v", tt.expectErr, err)
			}
			if val != tt.expectedVal {
				t.Errorf("expected value = %v, got %v", tt.expectedVal, val)
			}
		})
	}
}

func TestReadMemoryUsageProc(t *testing.T) {
	tempDir := t.TempDir()
	procStatusPath = filepath.Join(tempDir, "status")
	defer func() { procStatusPath = "/proc/self/status" }()

	tests := []struct {
		name        string
		fileContent string
		expectedVal int64
		expectErr   bool
	}{
		{
			name:        "valid VmRSS",
			fileContent: "Name: proc\nVmRSS:     50000 kB\n",
			expectedVal: 51200000,
			expectErr:   false,
		},
		{
			name:        "missing VmRSS",
			fileContent: "Name: proc\n",
			expectedVal: 0,
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(procStatusPath, []byte(tt.fileContent), 0644)
			val, err := readMemoryUsageProc()
			if (err != nil) != tt.expectErr {
				t.Errorf("expected error = %v, got %v", tt.expectErr, err)
			}
			if val != tt.expectedVal {
				t.Errorf("expected value = %v, got %v", tt.expectedVal, val)
			}
		})
	}
}

func TestReadMemoryUsageGo(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "read memory usage go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := readMemoryUsageGo()
			if val <= 0 {
				t.Errorf("expected positive memory, got %d", val)
			}
		})
	}
}

func TestParseCPULimit(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
	}{
		{
			name:     "limit in millicores",
			input:    "500m",
			expected: 0.5,
		},
		{
			name:     "limit in cores",
			input:    "2",
			expected: 2.0,
		},
		{
			name:     "empty limit fallback to NumCPU",
			input:    "",
			expected: float64(runtime.NumCPU()),
		},
		{
			name:     "invalid limit fallback to NumCPU",
			input:    "invalid",
			expected: float64(runtime.NumCPU()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := parseCPULimit(tt.input)
			if val != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, val)
			}
		})
	}
}

func TestCollectSystemMetrics(t *testing.T) {
	tempDir := t.TempDir()

	oldCpuStat := cpuStatPath
	oldMemCurrent := memCurrentPath
	defer func() {
		cpuStatPath = oldCpuStat
		memCurrentPath = oldMemCurrent
	}()

	cpuStatPath = filepath.Join(tempDir, "cpu.stat")
	memCurrentPath = filepath.Join(tempDir, "memory.current")

	tests := []struct {
		name        string
		cpuStat1    string
		cpuStat2    string
		memCurrent  string
		expectedLen int
	}{
		{
			name:        "collect system metrics successfully",
			cpuStat1:    "usage_usec 10000\n",
			cpuStat2:    "usage_usec 20000\n",
			memCurrent:  "104857600\n",
			expectedLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(cpuStatPath, []byte(tt.cpuStat1), 0644)
			os.WriteFile(memCurrentPath, []byte(tt.memCurrent), 0644)

			tracker := &MetricsTracker{}
			tracker.collectSystemMetrics()

			os.WriteFile(cpuStatPath, []byte(tt.cpuStat2), 0644)
			tracker.collectSystemMetrics()

			tracker.mu.RLock()
			metricsCount := len(tracker.systemMetrics)
			tracker.mu.RUnlock()

			if metricsCount != tt.expectedLen {
				t.Errorf("expected %d metric points, got %d", tt.expectedLen, metricsCount)
			}
		})
	}
}

func TestHandleMetricsAndLimits(t *testing.T) {
	// Populate globalTracker with mock data
	globalTracker.mu.Lock()
	globalTracker.systemMetrics = []SystemMetric{
		{Timestamp: time.Now().Add(-10 * time.Minute), CPU: 45.5, Memory: 104857600},
		{Timestamp: time.Now().Add(-5 * time.Minute), CPU: 50.0, Memory: 105857600},
	}
	globalTracker.requests = []MetricEntry{
		{Timestamp: time.Now().Add(-2 * time.Second), Duration: 0.15, Tokens: 100},
		{Timestamp: time.Now().Add(-1 * time.Second), Duration: 0.05, Tokens: 50},
	}
	globalTracker.mu.Unlock()

	os.Setenv("LIMITS_CPU", "1000m")
	os.Setenv("LIMITS_MEMORY", "1Gi")
	defer func() {
		os.Unsetenv("LIMITS_CPU")
		os.Unsetenv("LIMITS_MEMORY")
	}()

	tests := []struct {
		name           string
		endpoint       string
		method         string
		url            string
		expectedStatus int
	}{
		{
			name:           "limits GET success",
			endpoint:       "limits",
			method:         "GET",
			url:            "/api/limits",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "limits POST method not allowed",
			endpoint:       "limits",
			method:         "POST",
			url:            "/api/limits",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "metrics GET success 30m",
			endpoint:       "metrics",
			method:         "GET",
			url:            "/api/metrics?range=30m",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "metrics GET success 1h",
			endpoint:       "metrics",
			method:         "GET",
			url:            "/api/metrics?range=1h",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "metrics GET success 3h",
			endpoint:       "metrics",
			method:         "GET",
			url:            "/api/metrics?range=3h",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "metrics GET success default range",
			endpoint:       "metrics",
			method:         "GET",
			url:            "/api/metrics?range=default",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "metrics POST method not allowed",
			endpoint:       "metrics",
			method:         "POST",
			url:            "/api/metrics",
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, nil)
			rr := httptest.NewRecorder()

			if tt.endpoint == "limits" {
				HandleLimits(rr, req)
				if rr.Code != tt.expectedStatus {
					t.Errorf("HandleLimits expected status %d, got %d", tt.expectedStatus, rr.Code)
				}
				if tt.method == "GET" && rr.Code == http.StatusOK {
					var limits map[string]string
					json.Unmarshal(rr.Body.Bytes(), &limits)
					if limits["cpu"] != "1000m" || limits["memory"] != "1Gi" {
						t.Errorf("expected cpu=1000m memory=1Gi, got %v", limits)
					}
				}
			} else {
				HandleMetrics(rr, req)
				if rr.Code != tt.expectedStatus {
					t.Errorf("HandleMetrics expected status %d, got %d", tt.expectedStatus, rr.Code)
				}
			}
		})
	}
}
