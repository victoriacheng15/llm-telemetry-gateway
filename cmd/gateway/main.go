package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
)

const socketPath = "/tmp/shared/policy.sock"

func main() {
	fmt.Println("LLM Telemetry Gateway Go client starting...")
	payload := `{"prompt": "Diagnostic review: Client SSN is 123-45-6789"}` + "\n"

	response, err := dialAndSend(socketPath, payload)
	if err != nil {
		log.Fatalf("Error in UDS communication: %v", err)
	}

	fmt.Printf("Received response: %s", response)
}

// dialAndSend connects to the UDS socket, writes the payload, and returns the response.
func dialAndSend(path, payload string) (string, error) {
	conn, err := net.Dial("unix", path)
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
