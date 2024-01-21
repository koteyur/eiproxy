package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	MasterAddr string
	ServerURL  string
	UserKey    string
}

var (
	cfg           config
	defaultConfig = config{
		ServerURL:  webSite,
		MasterAddr: "vps.gipat.ru:28004",
		UserKey:    userKeyPlaceholder,
	}
)

// getConfigPath returns path to config file in the same directory as executable.
func getConfigPath() string {
	exePath, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	dir := filepath.Dir(exePath)
	return filepath.Join(dir, "eiproxy.json")
}

func loadConfig() {
	configPath := getConfigPath()

	// Load eiproxy.json and if it doesn't exist create new one using default.
	// If fail, show message box and exit.
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = json.MarshalIndent(defaultConfig, "", "  ")
			if err != nil {
				fatal(err)
			}
			err = os.WriteFile(configPath, data, 0644)
			if err != nil {
				fatal(err)
			}
			cfg = defaultConfig
			return
		}
		fatal(err)
	}

	// Try to unmarshal config file.
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		fatal(err)
	}

	if cfg.UserKey == userKeyPlaceholder {
		cfg.UserKey = ""
	}

	cfg.UserKey = normalizeKey(cfg.UserKey)
}

func saveConfig() {
	cfg.UserKey = normalizeKey(cfg.UserKey)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fatal(err)
	}
	err = os.WriteFile(getConfigPath(), data, 0644)
	if err != nil {
		fatal(err)
	}
}

func normalizeKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}
