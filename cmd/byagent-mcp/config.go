package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultAPI = "https://app.byagent.dev"

// config holds API credentials. Env wins over ~/.artifacts/config.json.
type config struct {
	Token string
	API   string
}

type fileConfig struct {
	Token string `json:"token"`
	API   string `json:"api"`
}

func loadConfig() (config, error) {
	token := firstNonEmpty(
		os.Getenv("ARTIFACTS_TOKEN"),
		os.Getenv("BYAGENT_API_KEY"),
	)
	api := firstNonEmpty(
		os.Getenv("ARTIFACTS_API"),
		os.Getenv("BYAGENT_API"),
	)

	file, fileErr := readConfigFile()
	if token == "" {
		token = file.Token
	}
	if api == "" {
		api = file.API
	}
	if api == "" {
		api = defaultAPI
	}
	api = strings.TrimRight(api, "/")

	if token == "" {
		if fileErr != nil && !os.IsNotExist(fileErr) {
			return config{}, fmt.Errorf("cannot read %s: %w", configPath(), fileErr)
		}
		return config{}, fmt.Errorf("not configured: set ARTIFACTS_TOKEN (or BYAGENT_API_KEY), or run byagent login to write ~/.artifacts/config.json")
	}
	return config{Token: token, API: api}, nil
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".artifacts", "config.json")
	}
	return filepath.Join(home, ".artifacts", "config.json")
}

func readConfigFile() (fileConfig, error) {
	path := configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, err
	}
	var fc fileConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		return fileConfig{}, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return fc, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
