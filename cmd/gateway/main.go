package main

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
	"syscall"
	"time"
)

var socketPath = "/tmp/shared/policy.sock"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleCompletions)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Println("Go Completions Proxy listening on :8080...")
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	<-sigChan
	log.Println("Shutdown signal received. Stopping server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server graceful shutdown failed: %v", err)
	}
	log.Println("Server stopped cleanly.")
}

func handleCompletions(w http.ResponseWriter, r *http.Request) {
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
	response, err := dialAndSend(socketPath, payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "PII policy engine unreachable"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(response))
}

// dialAndSend connects to the UDS socket (with retry), writes the payload, and returns the response.
func dialAndSend(path, payload string) (string, error) {
	conn, err := dialWithRetry(path, 5, 100*time.Millisecond)
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

func dialWithRetry(path string, attempts int, backoff time.Duration) (net.Conn, error) {
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
