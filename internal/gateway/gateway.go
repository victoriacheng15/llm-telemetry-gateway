package gateway

import (
	"bufio"
	"context"
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
	SocketPath = "/tmp/shared/policy.sock"
	SigChan    = make(chan os.Signal, 1)

	meter         = otel.Meter("gateway")
	inputCounter  metric.Int64Counter
	outputCounter metric.Int64Counter
	durationHist  metric.Float64Histogram
)

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

	// Record input tokens
	inputTokens := CountTokens(payload)
	if inputCounter != nil {
		inputCounter.Add(r.Context(), inputTokens)
	}

	response, err := DialAndSend(SocketPath, payload)
	if err != nil {
		slog.Error("PII policy engine unreachable", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "PII policy engine unreachable"}`))
		return
	}

	// Record output tokens
	outputTokens := CountTokens(response)
	if outputCounter != nil {
		outputCounter.Add(r.Context(), outputTokens)
	}

	// Record duration histogram
	duration := time.Since(startTime).Seconds()
	if durationHist != nil {
		durationHist.Record(r.Context(), duration)
	}

	slog.Info("Request completed successfully",
		"duration_seconds", duration,
		"input_tokens", inputTokens,
		"output_tokens", outputTokens,
		"status", http.StatusOK,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(response))
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
