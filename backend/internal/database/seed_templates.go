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
	seed(
		"Meme 合约信号计算引擎 v7 — Day6 (Strict Trend Filter)",
		"Day6: 更保守的趋势确认版，只做高置信度、量价和 4h 方向更一致的机会（Redis 模式）",
		"meme_contract_signal_engine_v7_day6.py",
	)
	seed(
		"Meme 合约信号计算引擎 v8 — Day7 (Smart Adaptive Filter)",
		"Day7: 智能自适应过滤版，根据市场状态动态调整参数，增加开仓机会同时控制风险（Redis 模式）",
		"meme_contract_signal_engine_v8_day7.py",
	)
	seed(
		"Meme 合约信号计算引擎 v9 — Day8 (Quantum Contract Optimizer)",
		"Day8: 量子合约优化版，专为加密货币合约交易设计，集成资金费率、杠杆、流动性、市场操纵检测等智能调整（Redis 模式）",
		"meme_contract_signal_engine_v9_day8.py",
	)
	seed(
		"Meme 合约信号计算引擎 v10 — Day9 (Aggressive Filter)",
		"Day9: 激进过滤版，大幅降低开仓门槛，针对当前市场环境优化，解决开仓困难问题（Redis 模式）",
		"meme_contract_signal_engine_v10_day9.py",
	)
}
