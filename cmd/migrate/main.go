package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/storage/migrate"
	"github.com/synamcps/synamcps-server/internal/storage/pgconn"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: migrate [up|down]")
	}
	cmd := os.Args[1]

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.example.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	ctx := context.Background()
	pool, err := pgconn.NewPool(ctx, cfg.Metadata)
	if err != nil {
		log.Fatalf("postgres pool: %v", err)
	}
	defer pool.Close()

	switch cmd {
	case "up":
		err = migrate.Up(pool, "")
	case "down":
		err = migrate.Down(pool, "")
	default:
		log.Fatalf("unknown command %q (use up or down)", cmd)
	}
	if err != nil {
		log.Fatalf("migrate %s: %v", cmd, err)
	}
	fmt.Printf("migrate %s: ok\n", cmd)
}
