package database

import (
	"fmt"
	"log"
	"quanty_trade/internal/auth"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
		log.Fatal("Failed to connect to database:", err)
	}

	// Migrate user table first so we can bootstrap admin user safely.
	if err := DB.AutoMigrate(&models.User{}); err != nil {
		log.Fatal("Failed to migrate database:", err)
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
		if pw == "" {
			pw = "admin123"
		}
		hashedPassword, _ := auth.HashPassword(pw)
		admin = models.User{
			Username: adminUsername,
			Password: hashedPassword,
			Role:     models.RoleAdmin,
		}
		DB.Create(&admin)
	} else if adminPassword != "" {
		hashedPassword, _ := auth.HashPassword(adminPassword)
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
		&models.DailyPnL{},
	)
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

	if dbType == "mysql" {
		_ = DB.Exec(`
			UPDATE strategy_templates
			SET
				name = IF(name = '' OR name IS NULL, CONCAT('untitled_', REPLACE(UUID(), '-', '')), name),
				author_id = IF(author_id = 0, ?, author_id)
			WHERE name = '' OR name IS NULL OR author_id = 0
		`, admin.ID).Error
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
