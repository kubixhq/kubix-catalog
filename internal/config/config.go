package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DBHost              string
	DBPort              int
	DBName              string
	DBUser              string
	DBPassword          string
	DBSSLMode           string
	ServerPort          int
	SpecFetchTimeoutSec int
	MaxSpecSizeMB       int
}

func Load() Config {
	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))
	serverPort, _ := strconv.Atoi(getEnv("SERVER_PORT", "8082"))
	fetchTimeout, _ := strconv.Atoi(getEnv("SPEC_FETCH_TIMEOUT_SEC", "10"))
	maxSize, _ := strconv.Atoi(getEnv("MAX_SPEC_SIZE_MB", "10"))
	return Config{
		DBHost:              getEnv("DB_HOST", "localhost"),
		DBPort:              dbPort,
		DBName:              getEnv("DB_NAME", "kubix_catalog"),
		DBUser:              getEnv("DB_USER", "postgres"),
		DBPassword:          os.Getenv("DB_PASSWORD"),
		DBSSLMode:           getEnv("DB_SSL_MODE", "disable"),
		ServerPort:          serverPort,
		SpecFetchTimeoutSec: fetchTimeout,
		MaxSpecSizeMB:       maxSize,
	}
}

func (c Config) Validate() error {
	if c.DBPort <= 0 || c.DBPort > 65535 {
		return fmt.Errorf("DB_PORT must be between 1 and 65535 (got %q)", os.Getenv("DB_PORT"))
	}
	if c.ServerPort <= 0 || c.ServerPort > 65535 {
		return fmt.Errorf("SERVER_PORT must be between 1 and 65535 (got %q)", os.Getenv("SERVER_PORT"))
	}
	if c.SpecFetchTimeoutSec <= 0 {
		return fmt.Errorf("SPEC_FETCH_TIMEOUT_SEC must be positive")
	}
	if c.MaxSpecSizeMB <= 0 {
		return fmt.Errorf("MAX_SPEC_SIZE_MB must be positive")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
