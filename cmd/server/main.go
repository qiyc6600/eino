package main

import (
	"log"
	"os"

	"github.com/example/agent-eino-demo/internal/app"
)

func main() {
	cfg := app.LoadConfig()

	application := app.NewApp(cfg)

	// Create default threads for demo (threads are namespaced per user)
	for _, uid := range []string{"u_admin", "u_visitor"} {
		application.Runner.CreateThread(uid, "t_default")
		application.Runner.CreateThread(uid, "t_demo_1")
		application.Runner.CreateThread(uid, "t_demo_2")
	}

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
