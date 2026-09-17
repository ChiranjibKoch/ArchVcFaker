package config

import (
	"fmt"
	"os"
	"strconv"

	_ "github.com/joho/godotenv/autoload"

	"github.com/amarnathcjd/gogram/telegram"
)

var (
	ApiID    = getEnvInt("API_ID", 25021578)
	ApiHash  = getEnv("API_HASH", "dd92e0a3f0aa00f49ea0c26f441587b9")
	Token    = getEnv("BOT_TOKEN", "8597640971:AAFQGmtgiD8YYqpjQizSjMSMnUta80AuYQw")
	LoggerID = getEnvInt64("LOGGER_ID", -1003913393534)
	MongoURL = getEnv("MONGO_URL", "mongodb+srv://archpublic:v8KG2NlkAa70Fx3V@cluster0.whdnitw.mongodb.net/?appName=Cluster0")
	DbName = getEnv("DB_NAME", "xeydbbrc")

	OwnerID = getEnvInt64("OWNER_ID", 6303186145)
	Owner   *telegram.UserObj
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}

	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}

	return n
}

func Load() error {
	missing := []string{}
	if ApiID == 0 {
		missing = append(missing, "API_ID")
	}
	if ApiHash == "" {
		missing = append(missing, "API_HASH")
	}
	if Token == "" {
		missing = append(missing, "BOT_TOKEN")
	}
	if MongoURL == "" {
		missing = append(missing, "MONGO_URL")
	}
	if OwnerID == 0 {
		missing = append(missing, "OWNER_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %v", missing)
	}
	return nil
}

func ResolveOwner(client *telegram.Client) error {
	user, err := client.GetUser(OwnerID)
	if err != nil {
		return fmt.Errorf("failed to resolve owner (OWNER_ID=%d): %w", OwnerID, err)
	}
	if user == nil {
		return fmt.Errorf("failed to resolve owner (OWNER_ID=%d): user not found", OwnerID)
	}
	Owner = user
	return nil
}
