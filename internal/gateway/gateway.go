package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
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

func InitMetrics() {
	var err error
	inputCounter, err = meter.Int64Counter("gen_ai.usage.input_tokens",
		metric.WithDescription("Number of input tokens processed"),
	)
	if err != nil {
		log.Printf("Warning: failed to create inputCounter: %v", err)
	}

	outputCounter, err = meter.Int64Counter("gen_ai.usage.output_tokens",
		metric.WithDescription("Number of output tokens processed"),
	)
	if err != nil {
		log.Printf("Warning: failed to create outputCounter: %v", err)
	}

	durationHist, err = meter.Float64Histogram("gen_ai.client.request.duration_histogram",
		metric.WithDescription("Duration of client request in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Printf("Warning: failed to create durationHist: %v", err)
	}
}

func Run(serverAddr string) {
	// Initialize OpenTelemetry SDK
	ctx := context.Background()
	shutdown, err := initMeter(ctx)
	if err != nil {
		log.Printf("Warning: OpenTelemetry meter initialization failed: %v", err)
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(shutdownCtx); err != nil {
				log.Printf("Warning: OpenTelemetry shutdown failed: %v", err)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", HandleCompletions)

	srv := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}

	signal.Notify(SigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Go Completions Proxy listening on %s...", serverAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	<-SigChan
	log.Println("Shutdown signal received. Stopping server...")
	ctxGraceful, cancelGraceful := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelGraceful()
	if err := srv.Shutdown(ctxGraceful); err != nil {
		log.Fatalf("Server graceful shutdown failed: %v", err)
	}
	log.Println("Server stopped cleanly.")
}

func HandleCompletions(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Failed to read body"}`))
		return
	}

	payload := string(body) + "\n"

	// Record input tokens
	if inputCounter != nil {
		inputCounter.Add(r.Context(), CountTokens(payload))
	}

	response, err := DialAndSend(SocketPath, payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "PII policy engine unreachable"}`))
		return
	}

	// Record output tokens
	if outputCounter != nil {
		outputCounter.Add(r.Context(), CountTokens(response))
	}

	// Record duration histogram
	if durationHist != nil {
		durationHist.Record(r.Context(), time.Since(startTime).Seconds())
	}

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
