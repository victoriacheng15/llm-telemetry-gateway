package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"llm-telemetry-gateway/internal/web/showcase"
)

const port = ":3000"

// liveReloadSnippet is injected before </body> in every HTML response.
// It opens an SSE connection to /dev-reload. When the connection drops
// (because the server is restarting), it polls until the new server is up,
// then reloads the page.
const liveReloadSnippet = `<script>
(() => {
	function connect() {
		const es = new EventSource('/dev-reload');
		es.onerror = () => {
			es.close();
			const interval = setInterval(async () => {
				try {
					await fetch('/dev-reload');
					clearInterval(interval);
					location.reload();
				} catch (e) {}
			}, 200);
		};
	}
	connect();
})();
</script>`

func main() {
	log.Println("Starting LLM Telemetry Gateway Showcase Dev Server...")

	gen := showcase.NewGenerator(
		"internal/web/showcase/templates",
		"internal/web/showcase/templates/content",
		"dist",
	)

	if err := gen.Generate(); err != nil {
		log.Fatalf("initial build failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/dev-reload", sseHandler)
	mux.HandleFunc("/", serveHandler)

	log.Printf("dev server listening on http://localhost%s", port)
	if err := http.ListenAndServe(port, mux); err != nil {
		log.Fatalf("dev server failed: %v", err)
	}
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "data: connected\n\n")
	flusher.Flush()

	<-r.Context().Done()
}

func serveHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	filePath := filepath.Join("dist", path)

	if filepath.Ext(filePath) == ".html" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		html := string(content)
		if idx := strings.Index(html, "</body>"); idx != -1 {
			html = html[:idx] + liveReloadSnippet + html[idx:]
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
		return
	}

	http.FileServer(http.Dir("dist")).ServeHTTP(w, r)
}
