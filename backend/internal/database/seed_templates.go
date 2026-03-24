package database

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"quanty_trade/internal/conf"
	"quanty_trade/internal/models"
)

func SeedBuiltInTemplates(adminID uint) {
	if DB == nil || adminID == 0 {
		return
	}
	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		return
	}

	name := "Meme 合约信号计算引擎 v3 — Day2 (CVD+SR+动态止损)"
	filename := "meme_contract_signal_engine_v3_day2.py"
	absPath := filepath.Join(absDir, filename)
	b, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	code := strings.TrimSpace(string(b))
	if code == "" {
		return
	}

	var existing models.StrategyTemplate
	if err := DB.Where("name = ?", name).First(&existing).Error; err == nil {
		if strings.TrimSpace(existing.Code) == "" {
			_ = DB.Model(&models.StrategyTemplate{}).Where("id = ?", existing.ID).
				Updates(map[string]interface{}{"code": code, "path": filename, "updated_at": time.Now()}).Error
		}
		return
	}

	now := time.Now()
	_ = DB.Create(&models.StrategyTemplate{
		Name:        name,
		Description: "Day2: CVD 累积量差 + 支撑阻力自动识别 + 动态止损（Redis 模式）",
		Code:        code,
		Path:        filename,
		AuthorID:    adminID,
		TemplateType: "strategy",
		IsPublic:    false,
		IsDraft:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error
}

