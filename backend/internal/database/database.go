package database

import (
	"fmt"
	"log"
	"quanty_trade/internal/auth"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/models"

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
		log.Println("Connecting to MySQL database...")
	} else {
		path := c.DB.SqlitePath
		if path == "" {
			path = "quanty.db"
		}
		DB, err = gorm.Open(sqlite.Open(path), gormCfg)
		log.Println("Connecting to SQLite database (local)...")
	}

	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Auto Migrate: GORM will check if table exists and create/update schema automatically
	err = DB.AutoMigrate(
		&models.User{},
		&models.StrategyTemplate{},
		&models.StrategyInstance{},
		&models.StrategySelector{},
		&models.StrategySelectorChild{},
		&models.StrategyLog{},
		&models.APILog{},
		&models.Backtest{},
		&models.ExchangeOrderEvent{},
		&models.StrategyOrder{},
		&models.StrategyPosition{},
	)
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

	if dbType == "mysql" {
		_ = DB.Exec("ALTER TABLE strategy_templates DROP FOREIGN KEY fk_strategy_templates_author").Error
		_ = DB.Exec(`
			UPDATE strategy_templates
			SET
				name = IF(name = '' OR name IS NULL, CONCAT('template_', id), name),
				author_id = IF(author_id = 0, 1, author_id)
			WHERE name = '' OR name IS NULL OR author_id = 0
		`).Error
	} else {
		_ = DB.Exec(`
			UPDATE strategy_templates
			SET
				name = CASE WHEN name = '' OR name IS NULL THEN ('template_' || id) ELSE name END,
				author_id = CASE WHEN author_id = 0 THEN 1 ELSE author_id END
			WHERE name = '' OR name IS NULL OR author_id = 0
		`).Error
	}
	log.Println("Database schema is up to date.")

	adminUsername := c.Admin.Username
	if adminUsername == "" {
		adminUsername = "admin"
	}
	adminPassword := c.Admin.Password

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
}
