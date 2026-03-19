package database

import (
	"fmt"
	"log"
	"os"
	"quanty_trade/internal/auth"
	"quanty_trade/internal/models"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
	var err error
	dbType := os.Getenv("DB_TYPE")

	if dbType == "mysql" {
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASS"),
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
		)
		DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
		log.Println("Connecting to MySQL database...")
	} else {
		DB, err = gorm.Open(sqlite.Open("quanty.db"), &gorm.Config{})
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
		&models.StrategyLog{},
		&models.APILog{},
		&models.Backtest{},
		&models.ExchangeOrderEvent{},
	)
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}
	log.Println("Database schema is up to date.")

	// Create initial admin if not exists
	var admin models.User
	if err := DB.Where("username = ?", "admin").First(&admin).Error; err != nil {
		hashedPassword, _ := auth.HashPassword("admin123")
		admin = models.User{
			Username: "admin",
			Password: hashedPassword,
			Role:     models.RoleAdmin,
		}
		DB.Create(&admin)
		log.Println("Created default admin account: admin / admin123")
	}
}
