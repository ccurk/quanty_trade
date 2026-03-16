package database

import (
	"log"
	"quanty_trade/internal/auth"
	"quanty_trade/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
	var err error
	DB, err = gorm.Open(sqlite.Open("quanty.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Auto Migrate
	err = DB.AutoMigrate(&models.User{}, &models.StrategyTemplate{}, &models.StrategyInstance{})
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

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
