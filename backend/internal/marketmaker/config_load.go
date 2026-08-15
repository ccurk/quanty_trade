package marketmaker

import (
	"encoding/json"
	"os"

	"quanty_trade/internal/logger"
)

// LoadConfigFromEnv reads the market-maker config from the JSON file named by
// $MARKETMAKER_CONFIG. It returns a DISABLED config (Enabled=false) whenever the
// env is unset or the file is missing/invalid, so the whole module stays a strict
// no-op until the owner explicitly configures it. The file carries the API
// credentials the owner fills; keep it out of the image/repo.
func LoadConfigFromEnv() Config {
	path := os.Getenv("MARKETMAKER_CONFIG")
	if path == "" {
		return Config{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Warnf("[mm] config %s 读取失败,模块禁用: %v", path, err)
		return Config{}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		logger.Warnf("[mm] config %s 解析失败,模块禁用: %v", path, err)
		return Config{}
	}
	return cfg
}
