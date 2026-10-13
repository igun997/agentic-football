package main

import (
	"flag"
	"log"
	"net/http"

	"agentswars/internal/api"
	"agentswars/internal/room"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	web := flag.String("web", "../web/dist", "built spectator UI directory (optional)")
	flag.Parse()
	log.Printf("agents-wars listening on %s  (MCP: POST /mcp, API: /api/rooms)", *addr)
	log.Fatal(http.ListenAndServe(*addr, api.Handler(room.NewManager(), *web)))
}
