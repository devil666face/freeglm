package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Keys []string
}

func New() (*Config, error) {
	_key := os.Getenv("ZAI_API_KEY")
	if _key == "" {
		return &Config{}, fmt.Errorf("ZAI_API_KEY is empty the key from Authorization header will be used")
	}

	return &Config{
		Keys: strings.Split(_key, ","),
	}, nil
}
