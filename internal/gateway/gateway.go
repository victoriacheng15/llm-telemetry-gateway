package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var (
	SocketPath      = "/tmp/shared/policy.sock"
	DiagnosticsPath = "/tmp/shared/diagnostics.txt"
	RCALogPath      = "/tmp/shared/rca.log"
	SigChan         = make(chan os.Signal, 1)

	// OllamaURL is the base URL for the upstream Ollama LLM service.
	// Override via OLLAMA_URL env var for non-default deployments.
	OllamaURL    = "http://ollama.ollama.svc.cluster.local:11434"
	ollamaClient = &http.Client{Timeout: 30 * time.Second}

	// CompletionsURL is the local URL of the completions proxy.
	// Override via COMPLETIONS_URL env var or for testing.
	CompletionsURL = "http://localhost:8080"

	meter         = otel.Meter("gateway")
	inputCounter  metric.Int64Counter
	outputCounter metric.Int64Counter
	durationHist  metric.Float64Histogram
)

func init() {
	if url := os.Getenv("OLLAMA_URL"); url != "" {
		OllamaURL = url
	}
	if url := os.Getenv("COMPLETIONS_URL"); url != "" {
		CompletionsURL = url
	}
}

var globalTracker = &MetricsTracker{}

func HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rangeStr := r.URL.Query().Get("range")
	duration := 15 * time.Minute
	switch rangeStr {
	case "30m":
		duration = 30 * time.Minute
	case "1h":
		duration = time.Hour
	case "3h":
		duration = 3 * time.Hour
	}

	metrics := globalTracker.GetMetrics(duration)
	json.NewEncoder(w).Encode(metrics)
}

func init() {
	InitMetrics()
}

func initLogger() {
	levelStr := os.Getenv("LOG_LEVEL")
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))
}

func InitMetrics() {
	var err error
	inputCounter, err = meter.Int64Counter("gen_ai.usage.input_tokens",
		metric.WithDescription("Number of input tokens processed"),
	)
	if err != nil {
		slog.Warn("Failed to create inputCounter", "error", err)
	}

	outputCounter, err = meter.Int64Counter("gen_ai.usage.output_tokens",
		metric.WithDescription("Number of output tokens processed"),
	)
	if err != nil {
		slog.Warn("Failed to create outputCounter", "error", err)
	}

	durationHist, err = meter.Float64Histogram("gen_ai.client.request.duration_histogram",
		metric.WithDescription("Duration of client request in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		slog.Warn("Failed to create durationHist", "error", err)
	}
}

func Run(serverAddr string) {
	initLogger()

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	go globalTracker.StartCollector(bgCtx)
	go globalTracker.StartTelemetryEvaluator(bgCtx)

	// Initialize OpenTelemetry SDK
	ctx := context.Background()
	shutdown, err := initMeter(ctx)
	if err != nil {
		slog.Warn("OpenTelemetry meter initialization failed", "error", err)
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(shutdownCtx); err != nil {
				slog.Warn("OpenTelemetry shutdown failed", "error", err)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", HandleCompletions)
	mux.HandleFunc("/healthz", HandleHealthz)
	mux.HandleFunc("/readyz", HandleReadyz)
	mux.HandleFunc("/api/diagnostics", HandleDiagnostics)
	mux.HandleFunc("/api/logs/stream", HandleRCALogStream)
	mux.HandleFunc("/api/mask", HandleMaskTest)
	mux.HandleFunc("/api/limits", HandleLimits)
	mux.HandleFunc("/api/metrics", HandleMetrics)
	mux.Handle("/console/", http.StripPrefix("/console/", http.FileServer(http.Dir("internal/web/console"))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/console/", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}

	signal.Notify(SigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("Go Completions Proxy listening", "addr", serverAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("HTTP server failed to start or serve", "error", err)
			os.Exit(1)
		}
	}()

	<-SigChan
	slog.Info("Shutdown signal received. Stopping server...")
	ctxGraceful, cancelGraceful := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelGraceful()
	if err := srv.Shutdown(ctxGraceful); err != nil {
		slog.Error("Server graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Server stopped cleanly.")
}

func HandleCompletions(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	slog.Debug("Received chat completions request", "method", r.Method, "path", r.URL.Path)

	if r.Method != http.MethodPost {
		slog.Warn("Method not allowed", "method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Failed to read body"}`))
		return
	}

	payload := string(body) + "\n"

	isInternal := r.Header.Get("X-Gateway-Internal") == "true"

	// Record input tokens
	inputTokens := CountTokens(payload)
	if !isInternal && inputCounter != nil {
		inputCounter.Add(r.Context(), inputTokens)
	}

	maskedPayload, err := DialAndSend(SocketPath, payload)
	if err != nil {
		slog.Error("PII policy engine unreachable", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "PII policy engine unreachable"}`))
		return
	}

	slog.Debug("PII masking complete, forwarding to Ollama", "ollama_url", OllamaURL)

	// Forward the sanitized payload to the upstream Ollama LLM service.
	response, err := forwardToOllama(r.Context(), strings.TrimSpace(maskedPayload))
	if err != nil {
		slog.Error("Ollama upstream request failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error": "LLM upstream unreachable"}`))
		return
	}

	duration := time.Since(startTime).Seconds()

	if !isInternal {
		// Record output tokens from the Ollama response (not the masked payload)
		outputTokens := CountTokens(response)
		if outputCounter != nil {
			outputCounter.Add(r.Context(), outputTokens)
		}

		// Record duration histogram
		if durationHist != nil {
			durationHist.Record(r.Context(), duration)
		}

		globalTracker.RecordRequest(duration, inputTokens+outputTokens)
	}

	slog.Info("Request completed successfully",
		"duration_seconds", duration,
		"input_tokens", inputTokens,
		"status", http.StatusOK,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(response))
}

// forwardToOllama sends the sanitized payload to Ollama's OpenAI-compatible
// chat completions endpoint and returns the raw response body.
func forwardToOllama(ctx context.Context, maskedPayload string) (string, error) {
	url := OllamaURL + "/v1/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(maskedPayload))
	if err != nil {
		return "", fmt.Errorf("failed to build Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ollamaClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach Ollama at %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Ollama response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return string(respBody), nil
}

// DialAndSend connects to the UDS socket (with retry), writes the payload, and returns the response.
func DialAndSend(path, payload string) (string, error) {
	conn, err := DialWithRetry(path, 5, 100*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("failed to dial UDS socket %s: %w", path, err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte(payload))
	if err != nil {
		return "", fmt.Errorf("failed to write to UDS socket: %w", err)
	}

	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read response from UDS socket: %w", err)
	}

	return response, nil
}

func DialWithRetry(path string, attempts int, backoff time.Duration) (net.Conn, error) {
	var conn net.Conn
	var err error
	for i := 0; i < attempts; i++ {
		conn, err = net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		time.Sleep(backoff)
	}
	return nil, err
}

func CountTokens(text string) int64 {
	words := strings.Fields(text)
	if len(words) == 0 {
		return 1
	}
	return int64(len(words))
}

func initMeter(ctx context.Context) (func(context.Context) error, error) {
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("llm-telemetry-gateway"),
		),
	)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	return func(shutdownCtx context.Context) error {
		return meterProvider.Shutdown(shutdownCtx)
	}, nil
}

func HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy"}`))
}

func HandleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Test dialing the UDS socket to verify readiness
	conn, err := net.DialTimeout("unix", SocketPath, 100*time.Millisecond)
	if err != nil {
		slog.Warn("Readiness check failed: PII policy engine unreachable", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(fmt.Sprintf(`{"status": "unready", "reason": "PII policy engine unreachable: %v"}`, err)))
		return
	}
	conn.Close()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ready"}`))
}

func HandleDiagnostics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	data, err := os.ReadFile(DiagnosticsPath)
	if err != nil {
		if os.IsNotExist(err) {
			w.Write([]byte("Diagnostics not yet available."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Write(data)
}

func HandleLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cpuLimit := os.Getenv("LIMITS_CPU")
	if cpuLimit == "" {
		cpuLimit = "500m"
	}
	memLimit := os.Getenv("LIMITS_MEMORY")
	if memLimit == "" {
		memLimit = "512Mi"
	}

	response := map[string]string{
		"cpu":    cpuLimit,
		"memory": memLimit,
	}

	json.NewEncoder(w).Encode(response)
}

func HandleRCALogStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	file, err := os.Open(RCALogPath)
	if err != nil {
		file, err = os.OpenFile(RCALogPath, os.O_CREATE|os.O_RDONLY, 0644)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		fmt.Fprintf(w, "data: %s\n\n", strings.TrimSuffix(line, "\n"))
		flusher.Flush()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if err == io.EOF {
					break
				}
				if err != nil {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", strings.TrimSuffix(line, "\n"))
				flusher.Flush()
			}
		}
	}
}

func HandleMaskTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req MaskRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid request JSON"}`))
		return
	}

	prompt := req.Prompt
	if !strings.HasSuffix(prompt, "\n") {
		prompt += "\n"
	}

	startTime := time.Now()
	response, err := DialAndSend(SocketPath, prompt)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error": "PII policy engine unreachable: %v"}`, err)
		return
	}

	duration := time.Since(startTime).Seconds()
	tokens := CountTokens(prompt) + CountTokens(response)
	globalTracker.RecordRequest(duration, tokens)

	res := MaskResponse{
		Masked: strings.TrimSuffix(response, "\n"),
	}
	json.NewEncoder(w).Encode(res)
}
