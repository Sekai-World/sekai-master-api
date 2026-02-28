package main

import (
	"context"
	"log"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/storage"
	transport "sekai-master-api/internal/transport/http"
)

func main() {
	cfg := config.Load()

	db, err := storage.OpenDB(cfg)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close()

	keycloakVerifier, err := auth.NewKeycloakVerifier(context.Background(), cfg)
	if err != nil {
		log.Fatalf("failed to initialize keycloak verifier: %v", err)
	}

	router := transport.NewRouter(cfg, db, keycloakVerifier)

	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}
