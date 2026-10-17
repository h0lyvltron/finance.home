package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr     string
	DataDir  string
	DBPath   string
	SecureCookie bool
}

func Load() Config {
	addr := env("ADDR", ":3847")
	dataDir := env("DATA_DIR", "./data")
	dbPath := env("DB_PATH", dataDir+"/finance.db")
	secure, _ := strconv.ParseBool(env("SECURE_COOKIE", "false"))
	return Config{
		Addr:         addr,
		DataDir:      dataDir,
		DBPath:       dbPath,
		SecureCookie: secure,
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
