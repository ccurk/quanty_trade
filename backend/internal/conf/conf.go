package conf

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	yaml "github.com/goccy/go-yaml"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Network  NetworkConfig  `yaml:"network"`
	Paths    PathsConfig    `yaml:"paths"`
	DB       DBConfig       `yaml:"db"`
	Admin    AdminConfig    `yaml:"admin"`
	Security SecurityConfig `yaml:"security"`
	Exchange ExchangeConfig `yaml:"exchange"`
	Redis    RedisConfig    `yaml:"redis"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"`
}

type NetworkConfig struct {
	HTTPProxy  string `yaml:"http_proxy"`
	HTTPSProxy string `yaml:"https_proxy"`
	NoProxy    string `yaml:"no_proxy"`
}

type PathsConfig struct {
	LogsDir       string `yaml:"logs_dir"`
	StrategiesDir string `yaml:"strategies_dir"`
}

type DBConfig struct {
	Type       string `yaml:"type"`
	SqlitePath string `yaml:"sqlite_path"`
	User       string `yaml:"user"`
	Pass       string `yaml:"pass"`
	Host       string `yaml:"host"`
	Port       string `yaml:"port"`
	Name       string `yaml:"name"`
}

type AdminConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type SecurityConfig struct {
	JWTSecret           string `yaml:"jwt_secret"`
	ConfigEncryptionKey string `yaml:"config_encryption_key"`
}

type ExchangeConfig struct {
	Name    string        `yaml:"name"`
	Binance BinanceConfig `yaml:"binance"`
}

type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	Prefix   string `yaml:"prefix"`
}

type BinanceConfig struct {
	APIKey    string `yaml:"api_key"`
	APISecret string `yaml:"api_secret"`
	Testnet   bool   `yaml:"testnet"`
	Market    string `yaml:"market"`
	BaseURL   string `yaml:"base_url"`
	WsBaseURL string `yaml:"ws_base_url"`
	WsAPIURL  string `yaml:"wsapi_url"`
}

var (
	mu      sync.RWMutex
	loaded  bool
	cfg     Config
	cfgMap  map[string]interface{}
	rootDir string
)

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
			Mode: "debug",
		},
		Network: NetworkConfig{
			HTTPProxy:  "",
			HTTPSProxy: "",
			NoProxy:    "127.0.0.1,localhost",
		},
		Paths: PathsConfig{
			LogsDir:       "logs",
			StrategiesDir: "strategies",
		},
		DB: DBConfig{
			Type:       "sqlite",
			SqlitePath: "backend/quanty.db",
			Host:       "127.0.0.1",
			Port:       "3306",
			Name:       "quanty_trade",
		},
		Admin: AdminConfig{
			Username: "admin",
			Password: "",
		},
		Security: SecurityConfig{
			JWTSecret:           "",
			ConfigEncryptionKey: "",
		},
		Exchange: ExchangeConfig{
			Name: "mock",
			Binance: BinanceConfig{
				APIKey:    "",
				APISecret: "",
				Testnet:   false,
				Market:    "spot",
				BaseURL:   "",
				WsBaseURL: "",
				WsAPIURL:  "",
			},
		},
		Redis: RedisConfig{
			Enabled:  false,
			Addr:     "127.0.0.1:6379",
			Password: "",
			DB:       0,
			Prefix:   "qt",
		},
	}
}

func repoRootFromWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidates := []string{wd, filepath.Dir(wd)}
	for _, c := range candidates {
		if st, err := os.Stat(filepath.Join(c, "conf")); err == nil && st.IsDir() {
			return c
		}
	}
	return wd
}

func resolveConfigPath(root string) string {
	profile := strings.ToLower(strings.TrimSpace(os.Getenv("QT_ENV")))
	if profile == "" {
		profile = strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	}
	if profile == "" {
		profile = strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	}
	if profile == "" {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("GIN_MODE"))) {
		case "release":
			profile = "pro"
		case "debug", "test":
			profile = "dev"
		}
	}

	if profile == "pro" || profile == "prod" || profile == "production" {
		p := filepath.Join(root, "conf", "conf_pro.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if profile == "dev" || profile == "test" || profile == "testing" || profile == "staging" {
		p := filepath.Join(root, "conf", "conf_dev.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

func Load() error {
	mu.Lock()
	defer mu.Unlock()

	if loaded {
		return nil
	}

	rootDir = repoRootFromWD()
	cfg = defaultConfig()
	cfgMap = map[string]interface{}{}

	cfgPath := resolveConfigPath(rootDir)
	if cfgPath != "" {
		b, err := os.ReadFile(cfgPath)
		if err != nil {
			return err
		}
		_ = yaml.Unmarshal(b, &cfgMap)
		_ = yaml.Unmarshal(b, &cfg)
	}

	applyEnvOverrides(&cfg)
	normalizePaths(&cfg, rootDir)

	loaded = true
	return nil
}

func MustLoad() {
	if err := Load(); err != nil {
		panic(err)
	}
}

func RootDir() string {
	mu.RLock()
	defer mu.RUnlock()
	return rootDir
}

func C() Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}

func Path(p string) string {
	r := RootDir()
	if r == "" {
		return p
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(r, p)
}

func GetString(path string, def string) string {
	v, ok := getByPath(path)
	if !ok {
		return def
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return def
		}
		return t
	default:
		return def
	}
}

func GetInt(path string, def int) int {
	v, ok := getByPath(path)
	if !ok {
		return def
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
		return def
	default:
		return def
	}
}

func GetBool(path string, def bool) bool {
	v, ok := getByPath(path)
	if !ok {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		default:
			return def
		}
	default:
		return def
	}
}

func getByPath(path string) (interface{}, bool) {
	mu.RLock()
	defer mu.RUnlock()

	if cfgMap == nil {
		return nil, false
	}

	parts := strings.Split(path, ".")
	var cur interface{} = cfgMap
	for _, p := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		nxt, ok := m[p]
		if !ok {
			return nil, false
		}
		cur = nxt
	}
	return cur, true
}

func applyEnvOverrides(c *Config) {
	if v := strings.TrimSpace(os.Getenv("SERVER_HOST")); v != "" {
		c.Server.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("SERVER_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Server.Port = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("GIN_MODE")); v != "" {
		c.Server.Mode = v
	}

	if v := strings.TrimSpace(os.Getenv("HTTP_PROXY")); v != "" {
		c.Network.HTTPProxy = v
	}
	if v := strings.TrimSpace(os.Getenv("HTTPS_PROXY")); v != "" {
		c.Network.HTTPSProxy = v
	}
	if v := strings.TrimSpace(os.Getenv("NO_PROXY")); v != "" {
		c.Network.NoProxy = v
	}

	if v := strings.TrimSpace(os.Getenv("LOG_DIR")); v != "" {
		c.Paths.LogsDir = v
	}
	if v := strings.TrimSpace(os.Getenv("STRATEGIES_DIR")); v != "" {
		c.Paths.StrategiesDir = v
	}

	if v := strings.TrimSpace(os.Getenv("DB_TYPE")); v != "" {
		c.DB.Type = v
	}
	if v := strings.TrimSpace(os.Getenv("DB_SQLITE_PATH")); v != "" {
		c.DB.SqlitePath = v
	}
	if v := strings.TrimSpace(os.Getenv("DB_USER")); v != "" {
		c.DB.User = v
	}
	if v := strings.TrimSpace(os.Getenv("DB_PASS")); v != "" {
		c.DB.Pass = v
	}
	if v := strings.TrimSpace(os.Getenv("DB_HOST")); v != "" {
		c.DB.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("DB_PORT")); v != "" {
		c.DB.Port = v
	}
	if v := strings.TrimSpace(os.Getenv("DB_NAME")); v != "" {
		c.DB.Name = v
	}

	if v := strings.TrimSpace(os.Getenv("ADMIN_USERNAME")); v != "" {
		c.Admin.Username = v
	}
	if v := os.Getenv("ADMIN_PASSWORD"); v != "" {
		c.Admin.Password = v
	}

	if v := os.Getenv("JWT_SECRET"); v != "" {
		c.Security.JWTSecret = v
	}
	if v := os.Getenv("CONFIG_ENCRYPTION_KEY"); v != "" {
		c.Security.ConfigEncryptionKey = v
	}

	if v := strings.TrimSpace(os.Getenv("EXCHANGE")); v != "" {
		c.Exchange.Name = v
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_API_KEY")); v != "" {
		c.Exchange.Binance.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_API_SECRET")); v != "" {
		c.Exchange.Binance.APISecret = v
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_TESTNET")); v != "" {
		c.Exchange.Binance.Testnet = strings.ToLower(v) == "true"
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_MARKET")); v != "" {
		c.Exchange.Binance.Market = v
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_BASE_URL")); v != "" {
		c.Exchange.Binance.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_WS_BASE_URL")); v != "" {
		c.Exchange.Binance.WsBaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("BINANCE_WSAPI_URL")); v != "" {
		c.Exchange.Binance.WsAPIURL = v
	}

	if v := strings.TrimSpace(os.Getenv("REDIS_ENABLED")); v != "" {
		c.Redis.Enabled = strings.ToLower(v) == "true"
	}
	if v := strings.TrimSpace(os.Getenv("REDIS_ADDR")); v != "" {
		c.Redis.Addr = v
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		c.Redis.Password = v
	}
	if v := strings.TrimSpace(os.Getenv("REDIS_DB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.Redis.DB = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("REDIS_PREFIX")); v != "" {
		c.Redis.Prefix = v
	}
}

func normalizePaths(c *Config, root string) {
	if root == "" {
		return
	}
	if c.Paths.LogsDir != "" && !filepath.IsAbs(c.Paths.LogsDir) {
		c.Paths.LogsDir = filepath.Join(root, c.Paths.LogsDir)
	}
	if c.Paths.StrategiesDir != "" && !filepath.IsAbs(c.Paths.StrategiesDir) {
		c.Paths.StrategiesDir = filepath.Join(root, c.Paths.StrategiesDir)
	}
	if c.DB.Type == "sqlite" && c.DB.SqlitePath != "" && !filepath.IsAbs(c.DB.SqlitePath) {
		c.DB.SqlitePath = filepath.Join(root, c.DB.SqlitePath)
	}
}

func EnsureLoaded() error {
	mu.RLock()
	ok := loaded
	mu.RUnlock()
	if ok {
		return nil
	}
	return errors.New("config not loaded")
}
