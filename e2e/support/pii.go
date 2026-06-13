package support

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"llm-telemetry-gateway/internal/gateway"
)

type TestState struct {
	ProxyAddr     string
	UDSSocketPath string
	UDSListener   net.Listener
	ProxyServer   *http.Server
	LastResponse  *http.Response
	LastBody      string
	UDSRunning    bool
	udsMutex      sync.Mutex
}

func (s *TestState) SetupUDS() error {
	s.udsMutex.Lock()
	defer s.udsMutex.Unlock()

	if s.UDSListener != nil {
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "godog-uds-")
	if err != nil {
		return err
	}
	s.UDSSocketPath = filepath.Join(tmpDir, "policy.sock")

	l, err := net.Listen("unix", s.UDSSocketPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		return err
	}
	s.UDSListener = l
	s.UDSRunning = true

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.handleUDSConnection(conn)
		}
	}()

	return nil
}

func (s *TestState) StopUDS() {
	s.udsMutex.Lock()
	defer s.udsMutex.Unlock()

	s.UDSRunning = false
	if s.UDSListener != nil {
		s.UDSListener.Close()
		s.UDSListener = nil
	}
	if s.UDSSocketPath != "" {
		os.Remove(s.UDSSocketPath)
		os.Remove(filepath.Dir(s.UDSSocketPath))
		s.UDSSocketPath = ""
	}
}

func (s *TestState) handleUDSConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		s.udsMutex.Lock()
		running := s.UDSRunning
		s.udsMutex.Unlock()
		if !running {
			return
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		masked := s.maskPII(line)
		if !strings.HasSuffix(masked, "\n") {
			masked += "\n"
		}

		_, err = conn.Write([]byte(masked))
		if err != nil {
			return
		}
	}
}

func (s *TestState) maskPII(input string) string {
	ssnRegex := regexp.MustCompile(`\d{3}-\d{2}-\d{4}`)
	sinRegex := regexp.MustCompile(`\d{3}-\d{3}-\d{3}`)
	ccRegex := regexp.MustCompile(`\d{4}-\d{4}-\d{4}-\d{4}`)

	out := ssnRegex.ReplaceAllString(input, "***-**-****")
	out = sinRegex.ReplaceAllString(out, "***-***-***")
	out = ccRegex.ReplaceAllString(out, "****-****-****-****")
	return out
}

func (s *TestState) SetupProxy() error {
	if s.ProxyServer != nil {
		return nil
	}

	gateway.SocketPath = s.UDSSocketPath

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", gateway.HandleCompletions)
	mux.HandleFunc("/healthz", gateway.HandleHealthz)
	mux.HandleFunc("/readyz", gateway.HandleReadyz)
	mux.HandleFunc("/api/mask", gateway.HandleMaskTest)

	server := &http.Server{
		Handler: mux,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	s.ProxyAddr = fmt.Sprintf("http://%s", listener.Addr().String())
	s.ProxyServer = server

	go func() {
		_ = server.Serve(listener)
	}()

	return nil
}

func (s *TestState) Cleanup() {
	s.StopUDS()
	if s.ProxyServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.ProxyServer.Shutdown(ctx)
		s.ProxyServer = nil
	}
}

func InitializeScenario(ctx *godog.ScenarioContext, state *TestState) {
	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		return ctx, nil
	})

	ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		state.Cleanup()
		return ctx, nil
	})
}
