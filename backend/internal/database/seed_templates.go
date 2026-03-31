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

	seed := func(name string, description string, filename string) {
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
			if strings.TrimSpace(existing.Code) == "" || strings.TrimSpace(existing.Path) == "" {
				_ = DB.Model(&models.StrategyTemplate{}).Where("id = ?", existing.ID).
					Updates(map[string]interface{}{"code": code, "path": filename, "updated_at": time.Now()}).Error
			}
			return
		}
		now := time.Now()
		_ = DB.Create(&models.StrategyTemplate{
			Name:         name,
			Description:  description,
			Code:         code,
			Path:         filename,
			AuthorID:     adminID,
			TemplateType: "strategy",
			IsPublic:     false,
			IsDraft:      false,
			CreatedAt:    now,
			UpdatedAt:    now,
		}).Error
	}

	seed(
		"Meme 合约信号计算引擎 v2（Redis 模式）",
		"V2: RedisSignal 协议最小实现（ready + candle 订阅 + open 信号）",
		"meme_contract_signal_engine_v2.py",
	)
	seed(
		"Meme 合约信号计算引擎 v3 — Day2 (CVD+SR+动态止损)",
		"Day2: CVD 累积量差 + 支撑阻力自动识别 + 动态止损（Redis 模式）",
		"meme_contract_signal_engine_v3_day2.py",
	)
	seed(
		"Meme 合约信号计算引擎 v6 — Day5 (Regime+ScoreCard)",
		"Day5: 市场状态识别 + 自适应参数 + 综合评分卡（Redis 模式）",
		"meme_contract_signal_engine_v6_day5.py",
	)
}
