package markoutdb

import (
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/marketmaker"
	"quanty_trade/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(0)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.MarkoutFill{}, &models.StrategyParamVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// cache=shared 让同进程内多个连接共享同一份内存库,所以每个用例先清干净。
	db.Exec("DELETE FROM markout_fills")
	db.Exec("DELETE FROM strategy_param_versions")
	return db
}

func sampleRecord() marketmaker.MarkoutRecord {
	t0 := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	side, fillPx := "sell", 200.0
	mk := func(mid float64) float64 { return marketmaker.MarkoutBps(side, fillPx, mid) }
	return marketmaker.MarkoutRecord{
		Exchange: "gate", Symbol: "ONG_USDT", FillID: "12345",
		Side: side, FillPx: fillPx, Amount: 3, FeeBps: 20,
		FillTs:   t0,
		Complete: true,
		Points: []marketmaker.MarkoutPoint{
			{Horizon: "1s", Mid: 199.80, SampleTs: t0.Add(1200 * time.Millisecond), LagMs: 200, Bps: mk(199.80)},
			{Horizon: "5s", Mid: 199.60, SampleTs: t0.Add(5 * time.Second), LagMs: 0, Bps: mk(199.60)},
			{Horizon: "30s", Mid: 201.00, SampleTs: t0.Add(30 * time.Second), LagMs: 0, Bps: mk(201.00)},
		},
		ResolvedAt: t0.Add(30 * time.Second),
	}
}

// TestWriteMarkoutIsAppendOnly:同一笔成交重复写只留一行,且【先写的那行赢】。
//
// 为什么必须是 DO NOTHING 而不是 DO UPDATE:引擎每 10s 重拉最近 100 笔成交,
// 同一笔会被反复看到。若后写覆盖先写,这张表就又有了"当前值"语义 ——
// 正是台账 #42 要甩掉的那个形状(读了几万次,只留最后一次)。
func TestWriteMarkoutIsAppendOnly(t *testing.T) {
	db := testDB(t)
	s := &Store{db: db, params: map[string]paramRef{}}

	rec := sampleRecord()
	if err := s.WriteMarkout(rec); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	// 同一个 fill_id 再写一次,而且带着不同的数值(模拟重拉/重算)
	again := sampleRecord()
	again.FillPx = 999
	for i := range again.Points {
		again.Points[i].Mid = 999
		again.Points[i].Bps = marketmaker.MarkoutBps(again.Side, again.FillPx, again.Points[i].Mid)
	}
	if err := s.WriteMarkout(again); err != nil {
		t.Fatalf("重复写入应当静默 no-op,却报错: %v", err)
	}

	var rows []models.MarkoutFill
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("查表失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("(exchange,symbol,fill_id) 唯一,应只有 1 行,得到 %d 行", len(rows))
	}
	if rows[0].FillPx != 200 {
		t.Fatalf("先写的那行必须原样保留(append-only),fill_px 被改成了 %v", rows[0].FillPx)
	}
	if rows[0].HorizonsDone != 3 || !rows[0].Complete {
		t.Fatalf("完整记录应落成 complete=true/horizons_done=3,得到 %+v", rows[0])
	}
	if rows[0].Mid1s != 199.80 || rows[0].Lag1sMs != 200 {
		t.Fatalf("原始中价与采样滞后没落对: mid_1s=%v lag=%d", rows[0].Mid1s, rows[0].Lag1sMs)
	}
}

// TestWriteMarkoutRejectsInconsistentRow:bps 与自带原始量对不上的记录一律拒收。
// 这种行比缺一行更坏 —— 它看起来可复核,其实不是。
func TestWriteMarkoutRejectsInconsistentRow(t *testing.T) {
	db := testDB(t)
	s := &Store{db: db, params: map[string]paramRef{}}

	bad := sampleRecord()
	bad.Points[0].Bps += 5 // 篡改
	if err := s.WriteMarkout(bad); err == nil {
		t.Fatal("bps 与原始量不一致的记录必须被拒")
	}
	var n int64
	db.Model(&models.MarkoutFill{}).Count(&n)
	if n != 0 {
		t.Fatalf("被拒的记录不该留下任何行,得到 %d 行", n)
	}

	if err := s.WriteMarkout(marketmaker.MarkoutRecord{Symbol: "X", Side: "buy"}); err == nil {
		t.Fatal("缺 fill_id 的记录必须被拒 —— 没有成交标识就无法事后复核")
	}
}

// TestPersistedRowIsRecheckableInPureSQL 是"离线复算不会漂移"的最终形态。
//
// 复核方不需要跑 Go、不需要凭据、不需要重写一遍公式:直接对着落好的库跑这段 SQL,
// 就能验证每一行的 markout 与它自己的原始量自洽。这段 SQL 与
// scripts/markout_persistence.sql 里给所有者的那段是同一条,在这里被跑过一次。
func TestPersistedRowIsRecheckableInPureSQL(t *testing.T) {
	db := testDB(t)
	s := &Store{db: db, params: map[string]paramRef{}}
	if err := s.WriteMarkout(sampleRecord()); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	buy := sampleRecord()
	buy.FillID, buy.Side = "67890", "buy"
	for i := range buy.Points {
		buy.Points[i].Bps = marketmaker.MarkoutBps("buy", buy.FillPx, buy.Points[i].Mid)
	}
	if err := s.WriteMarkout(buy); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	const recheck = `
		SELECT COUNT(*) FROM markout_fills
		WHERE mid_1s > 0 AND ABS(
		  (CASE WHEN side = 'sell' THEN -1 ELSE 1 END)
		  * (mid_1s - fill_px) / fill_px * 10000 - markout_bps_1s) > 1e-6`
	var mismatched int64
	if err := db.Raw(recheck).Scan(&mismatched).Error; err != nil {
		t.Fatalf("复核 SQL 跑不通: %v", err)
	}
	if mismatched != 0 {
		t.Fatalf("有 %d 行的 markout 与自带原始量对不上", mismatched)
	}
	var total int64
	db.Model(&models.MarkoutFill{}).Count(&total)
	if total != 2 {
		t.Fatalf("应有 2 行(买/卖各一),得到 %d", total)
	}
}

func mmConfig(spread float64) marketmaker.Config {
	return marketmaker.Config{
		Feed: "binance",
		Exec: []marketmaker.ExecConfig{{
			Name: "gate", APIKey: "SECRET-KEY-DO-NOT-PERSIST",
			APISecret: "SECRET-SECRET-DO-NOT-PERSIST", Passphrase: "SECRET-PASS",
		}},
		Pairs: []marketmaker.PairConfig{{
			FeedSymbol: "ONGUSDT", Exec: "gate", ExecSymbol: "ONG_USDT",
			SpreadBps: spread, OrderQty: 10, MaxPosition: 100, RefreshMs: 1000,
		}},
	}
}

// TestParamVersionIsAppendOnlyAndSecretFree:参数版本挂在既有的
// strategy_param_versions 上,同参数不开新版、改参数才开新版,且绝不写入凭据。
func TestParamVersionIsAppendOnlyAndSecretFree(t *testing.T) {
	db := testDB(t)
	cfg := mmConfig(30)

	first := ensureParamVersion(db, cfg, cfg.Pairs[0])
	if first.id == nil || first.hash == "" {
		t.Fatalf("首次应登记出一个版本,得到 %+v", first)
	}
	// 参数没变:复用同一版,不写新行
	same := ensureParamVersion(db, cfg, cfg.Pairs[0])
	if same.id == nil || *same.id != *first.id || same.hash != first.hash {
		t.Fatalf("参数未变不该开新版: first=%v same=%v", *first.id, same.id)
	}
	var n int64
	db.Model(&models.StrategyParamVersion{}).Count(&n)
	if n != 1 {
		t.Fatalf("参数未变时应只有 1 行,得到 %d", n)
	}

	// 参数变了:开新版,旧版被关窗(append-only,旧行不被改写成新参数)
	cfg2 := mmConfig(45)
	second := ensureParamVersion(db, cfg2, cfg2.Pairs[0])
	if second.id == nil || *second.id == *first.id {
		t.Fatalf("参数变化必须开新版,得到 %v", second.id)
	}
	var rows []models.StrategyParamVersion
	db.Order("id asc").Find(&rows)
	if len(rows) != 2 {
		t.Fatalf("应有 2 个版本,得到 %d", len(rows))
	}
	if rows[0].IsCurrent || rows[0].EffectiveTo == nil {
		t.Fatalf("旧版必须被关窗(is_current=false 且 effective_to 有值): %+v", rows[0])
	}
	if !rows[1].IsCurrent || rows[1].Seq != 2 || rows[1].Label != "v2" {
		t.Fatalf("新版应是 current/seq=2/v2: %+v", rows[1])
	}
	if !strings.Contains(rows[0].ConfigJSON, `"spread_bps":30`) {
		t.Fatalf("旧版应仍然记着旧参数,得到 %s", rows[0].ConfigJSON)
	}
	if rows[0].StrategyID != "mm:gate:ONG_USDT" {
		t.Fatalf("做市参数版本应挂在合成 strategy_id 上,得到 %s", rows[0].StrategyID)
	}

	// 凭据一个字节都不能进这张表 —— 它是长期保留的。
	for _, r := range rows {
		for _, secret := range []string{"SECRET-KEY", "SECRET-SECRET", "SECRET-PASS"} {
			if strings.Contains(r.ConfigJSON, secret) {
				t.Fatalf("参数版本里出现了凭据(%s): %s", secret, r.ConfigJSON)
			}
		}
	}
}

// TestParamVersionStampedOntoRow:落库的每行都带上参数版本,事后能回答
// "这个 markout 是在哪一套参数下测出来的"。
func TestParamVersionStampedOntoRow(t *testing.T) {
	db := testDB(t)
	cfg := mmConfig(30)
	ref := ensureParamVersion(db, cfg, cfg.Pairs[0])
	s := &Store{db: db, params: map[string]paramRef{"gate|ONG_USDT": ref}}

	if err := s.WriteMarkout(sampleRecord()); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var row models.MarkoutFill
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("查表失败: %v", err)
	}
	if row.ParamHash != ref.hash {
		t.Fatalf("param_hash 没落上: %q vs %q", row.ParamHash, ref.hash)
	}
	if row.ParamVersionID == nil || *row.ParamVersionID != *ref.id {
		t.Fatalf("param_version_id 没落上: %v", row.ParamVersionID)
	}

	// 版本表不可用时(登记失败),仍然要落行,只是版本号留空 —— 缺一个戳
	// 可以退化成按 param_hash 归组,填一个错的戳则无法补救。
	s2 := &Store{db: db, params: map[string]paramRef{}}
	other := sampleRecord()
	other.FillID = "99999"
	if err := s2.WriteMarkout(other); err != nil {
		t.Fatalf("无参数版本时也必须能落库: %v", err)
	}
	var row2 models.MarkoutFill
	db.Where("fill_id = ?", "99999").First(&row2)
	if row2.ParamVersionID != nil {
		t.Fatalf("拿不到版本时必须留 NULL,不能瞎填: %v", *row2.ParamVersionID)
	}
}
