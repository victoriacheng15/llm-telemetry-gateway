package main

import (
	"llm-telemetry-gateway/internal/gateway"
)

func main() {
	gateway.Run(":8080")
}
