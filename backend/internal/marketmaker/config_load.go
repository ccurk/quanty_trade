package marketmaker

import (
	"encoding/json"
	"os"
	"strings"

	"quanty_trade/internal/logger"
)

// LoadConfigFromEnv builds the market-maker config from environment (fits a .env
// workflow) with this precedence for the STRUCTURE (pairs/feed/exec names/flags):
//
//	1. $MARKETMAKER_CONFIG_JSON — the whole config inline as a JSON string, or
//	2. $MARKETMAKER_CONFIG       — path to a JSON file.
//
// SECRETS are then overlaid from discrete env vars so keys never live in the JSON
// (or the repo): for each exec entry, an empty api_key/api_secret is filled from
//
//	MM_<NAME>_API_KEY / MM_<NAME>_API_SECRET   (NAME = exec.name upper-cased)
//	e.g. MM_COINSPH_API_KEY, MM_MEXC_API_SECRET.
//
// With none of these set the module is a strict no-op (Enabled stays false).
func LoadConfigFromEnv() Config {
	var cfg Config
	loaded := false

	if raw := strings.TrimSpace(os.Getenv("MARKETMAKER_CONFIG_JSON")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			logger.Warnf("[mm] MARKETMAKER_CONFIG_JSON 解析失败,模块禁用: %v", err)
			return Config{}
		}
		loaded = true
	} else if path := strings.TrimSpace(os.Getenv("MARKETMAKER_CONFIG")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			logger.Warnf("[mm] config %s 读取失败,模块禁用: %v", path, err)
			return Config{}
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Warnf("[mm] config %s 解析失败,模块禁用: %v", path, err)
			return Config{}
		}
		loaded = true
	}

	if !loaded {
		return Config{}
	}

	// Overlay secrets from discrete env vars (keeps keys out of the JSON/repo).
	for i := range cfg.Exec {
		up := strings.ToUpper(strings.TrimSpace(cfg.Exec[i].Name))
		if cfg.Exec[i].APIKey == "" {
			cfg.Exec[i].APIKey = os.Getenv("MM_" + up + "_API_KEY")
		}
		if cfg.Exec[i].APISecret == "" {
			cfg.Exec[i].APISecret = os.Getenv("MM_" + up + "_API_SECRET")
		}
		if cfg.Exec[i].Passphrase == "" {
			cfg.Exec[i].Passphrase = os.Getenv("MM_" + up + "_API_PASSPHRASE")
		}
	}
	return cfg
}
