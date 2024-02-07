package main

import (
	"context"
	"eiproxy/client"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var (
	mode       = flag.String("mode", "server", "Mode to run in (client or server)")
	configPath = flag.String("config", "", "Path to config file. By default uses mode name + .json")
)

func main() {
	flag.Parse()

	if *configPath == "" {
		*configPath = *mode + ".json"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	if *mode == "client" {
		cfg := client.DefaultConfig
		readConfig(*configPath, &cfg)
		err = client.New(cfg).Run(ctx)
	} else if *mode == "server" {
		log.Fatalf("Will be available soon")
	} else {
		log.Fatalf("Unknown mode %q", *mode)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		message := "Error:\n"
		for _, e := range strings.Split(err.Error(), "\n") {
			message += fmt.Sprintf(" - %s", e)
		}
		log.Println(message)
		os.Exit(1)
	}
}

func readConfig(path string, cfg any) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("Config file not found, saving default config to %s", path)

			data, err = json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				log.Fatalf("Failed to marshal default config: %v", err)
			}

			err = os.WriteFile(path, data, 0644)
			if err != nil {
				log.Fatalf("Failed to write default config: %v", err)
			}
			return
		}
		log.Fatalf("Failed to read config: %v", err)
	}

	err = json.Unmarshal(data, cfg)
	if err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}
}
