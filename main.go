package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	_ "modernc.org/sqlite"
)

const (
	charset = "abcdefghkprstxyz2345678"
	codeLen = 6
)

func main() {
	if err := initDB(); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	if err := loadSettings(); err != nil {
		log.Fatalf("failed to load settings: %v", err)
	}

	pb, ph, uh, ih, ah := cfg.snapshot()
	papiHost := cfg.publicAPIHostVal()
	log.Printf("public: %s (%s)  ui: %s  internal: %s  alias: %s  public-api: %s", pb, ph, uh, ih, ah, papiHost)

	webMux := http.NewServeMux()
	webMux.HandleFunc("/", mainHandler)
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", newMCPHandler())
	mcpHTTPServer := &http.Server{
		Addr:              mcpPort,
		Handler:           mcpMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}

	serverErrors := make(chan error, 2)
	go func() {
		log.Printf("web: listening on %s", port)
		serverErrors <- fmt.Errorf("web server: %w", http.ListenAndServe(port, webMux))
	}()
	go func() {
		log.Printf("mcp: listening without authentication on %s/mcp", mcpPort)
		serverErrors <- fmt.Errorf("MCP server: %w", mcpHTTPServer.ListenAndServe())
	}()
	log.Fatal(<-serverErrors)
}
