package main

import (
	"log"

	"llm-telemetry-gateway/internal/web/showcase"
)

func main() {
	log.Println("Starting LLM Telemetry Gateway Showcase Generator...")

	gen := showcase.NewGenerator(
		"internal/web/showcase/templates",
		"internal/web/showcase/templates/content",
		"dist",
	)

	if err := gen.Generate(); err != nil {
		log.Fatalf("Failed to generate static site: %v", err)
	}

	log.Println("Showcase site generated successfully in dist/")
}
