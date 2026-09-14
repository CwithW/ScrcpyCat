package main

import (
	"log"
	"net/http"

	"github.com/cwithw/ScrcpyCat/backend/internal/controlplane"
)

func main() {
	config, err := controlplane.LoadConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	server, err := controlplane.NewServer(config)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()

	log.Printf("control plane listening on %s", config.ListenAddress)
	if err := http.ListenAndServe(config.ListenAddress, server.Handler()); err != nil {
		log.Fatal(err)
	}
}
