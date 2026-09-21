package main

import (
	"log"
	"os"

	"github.com/example/agent-eino-demo/internal/app"
)

func main() {
	cfg := app.LoadConfig()

	application := app.NewApp(cfg)
	defer application.Close()

	port := os.Getenv("PORT")
	if port != "" {
		cfg.Addr = ":" + port
	}

	log.Printf("=== Agent Eino Demo ===")
	log.Printf("Server starting on %s", cfg.Addr)
	log.Printf("Open http://localhost%s in your browser", cfg.Addr)

	if err := application.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
