package database

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"quanty_trade/internal/auth"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/lark"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fatalAlert 在进程因致命启动错误退出前，尽力把消息推到 Lark，再 log.Fatal。
// 启动期（DB 初始化）异步告警 loop 还没起，所以走 lark.AlertSync（已由 main 提前 Configure）。
func fatalAlert(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	lark.AlertSync("🚨 QuantyTrade 启动失败 · " + msg)
	log.Fatal(msg)
}

// generateRandomPassword 生成 n 字符长度的随机密码（base64-url-safe，无填充）。
// 用 crypto/rand 保证不可预测。n 至少 24，否则 base64 截断后熵不足。
func generateRandomPassword(n int) string {
	if n < 24 {
		n = 24
	}
	// base64 输出 4/3 倍长度，输入字节数取上整后再截到目标长度。
	b := make([]byte, (n*3+3)/4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见，但发生时绝不退回弱密码。
		fatalAlert("crypto/rand.Read failed: %v", err)
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

var DB *gorm.DB

// InitDB initializes the global GORM database connection and runs migrations.
//
// Environment variables:
// - DB_TYPE=mysql enables MySQL; otherwise SQLite is used (quanty.db in cwd)
// - DB_USER/DB_PASS/DB_HOST/DB_PORT/DB_NAME configure MySQL DSN
//
// Admin bootstrap:
// - If ADMIN_USERNAME is not set, defaults to "admin"
// - If ADMIN_PASSWORD is set:
//   - If admin user exists: password is reset to ADMIN_PASSWORD
//   - If admin user does not exist: user is created with ADMIN_PASSWORD
//
// - If ADMIN_PASSWORD is not set and admin user does not exist: defaults to "admin123"
func InitDB() {
	var err error
	c := conf.C()
	dbType := c.DB.Type
	gormCfg := &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true}

	if dbType == "mysql" {
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			c.DB.User,
			c.DB.Pass,
			c.DB.Host,
			c.DB.Port,
			c.DB.Name,
		)
		DB, err = gorm.Open(mysql.Open(dsn), gormCfg)
		logger.Infof("Connecting to MySQL database...")
	} else {
		path := c.DB.SqlitePath
		if path == "" {
			path = "quanty.db"
		}
		DB, err = gorm.Open(sqlite.Open(path), gormCfg)
		logger.Infof("Connecting to SQLite database (local)...")
	}

	if err != nil {
		fatalAlert("Failed to connect to database: %v", err)
	}

	// Migrate user table first so we can bootstrap admin user safely.
	if err := DB.AutoMigrate(&models.User{}); err != nil {
		fatalAlert("Failed to migrate database: %v", err)
	}

	adminUsername := c.Admin.Username
	if adminUsername == "" {
		adminUsername = "admin"
	}
	adminUsername = strings.TrimSpace(adminUsername)
	adminPassword := strings.TrimSpace(c.Admin.Password)

	var admin models.User
	if err := DB.Where("username = ?", adminUsername).First(&admin).Error; err != nil {
		pw := adminPassword
		pwSource := "ADMIN_PASSWORD/conf.admin.password"
		if pw == "" {
			// 关键修复：原来默认 "admin123"。现在生成 32 字符随机密码，
			// 一次性打到 log 里（启动者 grep 一下就能拿到），并大声告警
			// 让用户立刻通过 UI 改密。
			pw = generateRandomPassword(32)
			pwSource = "auto-generated (random)"
		}
		hashedPassword, herr := auth.HashPassword(pw)
		if herr != nil {
			fatalAlert("Failed to hash initial admin password: %v", herr)
		}
		admin = models.User{
			Username: adminUsername,
			Password: hashedPassword,
			Role:     models.RoleAdmin,
		}
		if err := DB.Create(&admin).Error; err != nil {
			fatalAlert("Failed to create initial admin user: %v", err)
		}
		if pwSource == "auto-generated (random)" {
			log.Printf("============================================================")
			log.Printf("[SECURITY] 已创建初始管理员账号 username=%s", adminUsername)
			log.Printf("[SECURITY] 自动生成的密码（仅本次启动可见）: %s", pw)
			log.Printf("[SECURITY] 请立刻登录后通过 UI 改掉这个密码。")
			log.Printf("[SECURITY] 后续重启不会再打印；需要重置请设置环境变量 ADMIN_PASSWORD 重启。")
			log.Printf("============================================================")
		} else {
			logger.Infof("Created initial admin user=%s password_source=%s", adminUsername, pwSource)
		}
	} else if adminPassword != "" {
		hashedPassword, herr := auth.HashPassword(adminPassword)
		if herr != nil {
			fatalAlert("Failed to hash admin password update: %v", herr)
		}
		DB.Model(&admin).Updates(map[string]interface{}{
			"password": hashedPassword,
			"role":     models.RoleAdmin,
		})
	}

	// Auto Migrate: GORM will check if table exists and create/update schema automatically
	err = DB.AutoMigrate(
		&models.StrategyTemplate{},
		&models.StrategyInstance{},
		&models.StrategyLog{},
		&models.APILog{},
		&models.Backtest{},
		&models.ExchangeOrderEvent{},
		&models.StrategyOrder{},
		&models.StrategyPosition{},
		&models.StrategyOptimizationRun{},
		&models.StrategyAuditLog{},
		&models.StrategyVersion{},
		&models.StrategyPublishRecord{},
		&models.DailyPnL{},
		&models.TelegramSubscriber{},
		&models.TelegramBotState{},
	)
	if err != nil {
		fatalAlert("Failed to migrate database: %v", err)
	}

	if dbType == "mysql" {
		// Widen strategy code columns TEXT(64KB) -> MEDIUMTEXT(16MB). GORM
		// AutoMigrate does not reliably widen an existing TEXT column, and a
		// >64KB code write would otherwise be silently truncated by MySQL.
		_ = DB.Exec("ALTER TABLE strategy_templates MODIFY code MEDIUMTEXT").Error
		_ = DB.Exec("ALTER TABLE strategy_versions MODIFY code MEDIUMTEXT").Error
		_ = DB.Exec(`
			UPDATE strategy_templates
			SET
				name = IF(name = '' OR name IS NULL, CONCAT('untitled_', REPLACE(UUID(), '-', '')), name),
				author_id = IF(author_id = 0, ?, author_id)
			WHERE name = '' OR name IS NULL OR author_id = 0
		`, admin.ID).Error

		// 一开仓一行(工业级去重,DB 层强制)。open_key 生成列:status='open' 时 =
		// owner:strategy:规范symbol(与 exchange.NormalizeSymbol 一致:大写、去 / 和 -),
		// 平仓自动变 NULL;唯一索引对 NULL 豁免 → 每 (owner,strategy,symbol) 至多一个
		// open 行。任何建行入口的重复 INSERT 都被 DB 拒(各入口本就忽略 create error →
		// 直接 no-op),从源头根治"一仓多行、PnL 散落",与代码路径无关、新增路径也自动受约束。
		// 幂等(按 information_schema 判定已加过就跳过)、best-effort(清洗/建索引失败只告警不
		// 阻断启动,运行期仍有 ③a 收养去重兜底)。
		var openKeyCol int
		_ = DB.Raw(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = 'strategy_positions' AND column_name = 'open_key'`).Scan(&openKeyCol).Error
		if openKeyCol == 0 {
			// 先清洗存量 open 重复(每组保留 id 最大者,其余置 closed),否则唯一索引建不起来。
			if err := DB.Exec(`
				UPDATE strategy_positions p
				JOIN (
					SELECT owner_id, strategy_id, UPPER(REPLACE(REPLACE(symbol,'/',''),'-','')) nsym, MAX(id) keep_id
					FROM strategy_positions WHERE status = 'open'
					GROUP BY owner_id, strategy_id, nsym
				) k ON p.owner_id = k.owner_id AND p.strategy_id = k.strategy_id
					AND UPPER(REPLACE(REPLACE(p.symbol,'/',''),'-','')) = k.nsym
					AND p.status = 'open' AND p.id <> k.keep_id
				SET p.status = 'closed', p.close_time = NOW(), p.updated_at = NOW()
			`).Error; err != nil {
				logger.Warnf("open_key 迁移: 存量去重失败(继续): %v", err)
			}
			if err := DB.Exec("ALTER TABLE strategy_positions ADD COLUMN open_key VARCHAR(160) " +
				"GENERATED ALWAYS AS (CASE WHEN status = 'open' THEN " +
				"CONCAT(owner_id, ':', strategy_id, ':', UPPER(REPLACE(REPLACE(symbol,'/',''),'-',''))) " +
				"ELSE NULL END) STORED").Error; err != nil {
				logger.Warnf("open_key 迁移: 加生成列失败(继续,退回 ③a 兜底): %v", err)
			} else if err := DB.Exec("CREATE UNIQUE INDEX uniq_open_position ON strategy_positions (open_key)").Error; err != nil {
				logger.Warnf("open_key 迁移: 建唯一索引失败(继续): %v", err)
			} else {
				logger.Infof("open_key 迁移: 一开仓一行唯一约束已就位")
			}
		}
	} else {
		_ = DB.Exec(`
			UPDATE strategy_templates
			SET
				name = CASE WHEN name = '' OR name IS NULL THEN ('untitled_' || lower(hex(randomblob(8)))) ELSE name END,
				author_id = CASE WHEN author_id = 0 THEN ? ELSE author_id END
			WHERE name = '' OR name IS NULL OR author_id = 0
		`, admin.ID).Error
	}
	SeedBuiltInTemplates(admin.ID)
	logger.Infof("Database schema is up to date.")
}
