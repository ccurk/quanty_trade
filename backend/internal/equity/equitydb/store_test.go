package equitydb

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/equity"
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
	if err := db.AutoMigrate(&models.EquitySnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// cache=shared 让同进程内多个连接共享同一份内存库,所以每个用例先清干净。
	db.Exec("DELETE FROM equity_snapshots")
	return db
}

func sampleSnapshot() equity.Snapshot {
	return equity.Snapshot{
		Venue:   equity.VenueBinanceUSDM,
		Asset:   "USDT",
		TakenAt: time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC),
		Free:    236.00,
		Total:   240.50,
		// 负的浮亏:必须能原样存下来。把它当成"缺失"抹成 0 会让权益系统性偏高。
		Unrealized: -4.25,
		Source:     "GET /fapi/v2/balance",
	}
}

// TestWriteIsAppendOnly:同一 (venue, asset, taken_at) 重复写只留一行,
// 且【先写的那行赢】。
//
// 为什么必须是 DO NOTHING 而不是 DO UPDATE:这张表存在的意义就是甩掉
// "当前值"语义(台账 #42:余额读了几万次,一次没落库,而且就算落也只落最后一次)。
// 后写覆盖先写就等于把那个形状又请回来了。
func TestWriteIsAppendOnly(t *testing.T) {
	db := testDB(t)
	s := &Store{db: db}

	first := sampleSnapshot()
	if err := s.WriteEquitySnapshot(first); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	// 同一时刻、同一场子、同一币种,但值不同 —— 不该覆盖。
	second := sampleSnapshot()
	second.Free, second.Total, second.Unrealized = 999, 999, 999
	if err := s.WriteEquitySnapshot(second); err != nil {
		t.Fatalf("重复写入不该报错(应为 no-op): %v", err)
	}

	var rows []models.EquitySnapshot
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("唯一键应当让重复写成为 no-op,得到 %d 行", len(rows))
	}
	if rows[0].Free != 236.00 || rows[0].Unrealized != -4.25 {
		t.Fatalf("先写的那行应当赢,得到 free=%v unrealized=%v", rows[0].Free, rows[0].Unrealized)
	}

	// 换一个时刻就是一条新的观测,必须能追加。
	later := sampleSnapshot()
	later.TakenAt = later.TakenAt.Add(time.Minute)
	later.Free = 300
	if err := s.WriteEquitySnapshot(later); err != nil {
		t.Fatalf("追加新观测失败: %v", err)
	}
	db.Find(&rows)
	if len(rows) != 2 {
		t.Fatalf("不同时刻应当是两行,得到 %d", len(rows))
	}
}

// TestRejectsUnlabelledRow:没有 venue 的行连存都不让存。
// equity.Emit 已经拦了一层,这里是第二层 —— 因为这张表最大的价值就是
// 每一行都知道自己是哪个场子的钱(#18/#22 撞车的真因)。
func TestRejectsUnlabelledRow(t *testing.T) {
	db := testDB(t)
	s := &Store{db: db}

	for _, tc := range []struct {
		name string
		mut  func(*equity.Snapshot)
	}{
		{"无 venue", func(s *equity.Snapshot) { s.Venue = "" }},
		{"无 asset", func(s *equity.Snapshot) { s.Asset = "" }},
		{"无 taken_at", func(s *equity.Snapshot) { s.TakenAt = time.Time{} }},
	} {
		snap := sampleSnapshot()
		tc.mut(&snap)
		if err := s.WriteEquitySnapshot(snap); err == nil {
			t.Errorf("%s 的快照应当被拒收", tc.name)
		}
	}

	var n int64
	db.Model(&models.EquitySnapshot{}).Count(&n)
	if n != 0 {
		t.Fatalf("被拒收的行不该落库,得到 %d 行", n)
	}
}

// TestNoDerivedTotalEquityColumn:这张表【没有】"总权益"这种列。
//
// 这条看起来像在测"某个字段不存在",但它守的是一个设计决定:总权益是
// 查询期的 SUM,落了库就会在口径一改时让历史行全废,并造出第二个真相源。
// 谁哪天顺手加一列 equity/total_usd,这条就会亮。
func TestNoDerivedTotalEquityColumn(t *testing.T) {
	db := testDB(t)
	types, err := db.Migrator().ColumnTypes(&models.EquitySnapshot{})
	if err != nil {
		t.Fatalf("取模型列失败: %v", err)
	}
	banned := map[string]bool{
		"equity": true, "total_equity": true, "equity_usd": true,
		"total_usd": true, "usd_value": true, "net_worth": true,
	}
	for _, c := range types {
		if banned[strings.ToLower(c.Name())] {
			t.Errorf("不该有派生列 %q —— 总权益是查询期的 SUM,不落库", c.Name())
		}
	}
}

// TestPersistedRowIsRecheckableInPureSQL:落好的行不靠这个程序也能复核。
// 一行自带 venue / 时刻 / free / total / unrealized / 来源端点,
// 跨场子求和是【相加】,这正是 §4 那条结论的可执行形式。
func TestPersistedRowIsRecheckableInPureSQL(t *testing.T) {
	db := testDB(t)
	s := &Store{db: db}

	// 两个场子各一行 —— 就是 #18/#22 撞车的那两个数。
	pm := equity.Snapshot{Venue: equity.VenuePolymarket, Asset: "USDC",
		TakenAt: time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC),
		Free:    57.60, Total: 57.60, Source: "GET /balance-allowance"}
	bn := sampleSnapshot() // binance_usdm, total 240.50, unrealized -4.25
	for _, r := range []equity.Snapshot{pm, bn} {
		if err := s.WriteEquitySnapshot(r); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}

	// 纯 SQL 求总权益:相加,不是三角定位。
	var got float64
	if err := db.Raw(
		`SELECT SUM(total + unrealized) FROM equity_snapshots`).Scan(&got).Error; err != nil {
		t.Fatalf("复核查询失败: %v", err)
	}
	want := 57.60 + 240.50 - 4.25
	if d := got - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("跨场子总权益应为 %.2f(相加),得到 %.2f", want, got)
	}

	// 每一行都答得出"这是哪个场子的钱、从哪个端点读来的"。
	var rows []models.EquitySnapshot
	db.Order("venue").Find(&rows)
	for _, r := range rows {
		if r.Venue == "" || r.Source == "" || r.TakenAt.IsZero() {
			t.Fatalf("行不可独立复核: %+v", r)
		}
	}
}

// TestDDLMatchesModel 把手写的建表脚本和 GORM 模型钉在一起。
//
// 为什么值得一条测试:equity_snapshots 不在 AutoMigrate 名单里(建表是所有者的事,
// 台账 #8),所以【没有任何运行时机制】会发现 DDL 和模型对不上 ——
// 表照建、程序照跑,只是每次 INSERT 报 unknown column,而那是在生产上才看得到的。
// 这与 markoutdb 的同名测试是同一条约定,两个功能共用一套形状。
func TestDDLMatchesModel(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "scripts", "equity_snapshots.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到建表脚本 %s: %v", path, err)
	}

	body, ok := createTableBody(string(raw), "equity_snapshots")
	if !ok {
		t.Fatal("脚本里找不到 CREATE TABLE equity_snapshots(...)")
	}
	ddlCols := ddlColumnNames(body)

	db := testDB(t)
	types, err := db.Migrator().ColumnTypes(&models.EquitySnapshot{})
	if err != nil {
		t.Fatalf("取模型列失败: %v", err)
	}
	modelCols := map[string]bool{}
	for _, c := range types {
		modelCols[c.Name()] = true
	}

	var missing, extra []string
	for c := range modelCols {
		if !ddlCols[c] {
			missing = append(missing, c)
		}
	}
	for c := range ddlCols {
		if !modelCols[c] {
			extra = append(extra, c)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("模型有、建表脚本缺的列(线上 INSERT 会报 unknown column): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("建表脚本有、模型没有的列(白建,或模型漏了字段): %v", extra)
	}

	// 唯一键决定"重复投递是 no-op 还是重复计数",漏了它整张表的统计都会偏。
	if !strings.Contains(body, "UNIQUE KEY idx_equity_snap_uniq (venue, asset, taken_at)") {
		t.Error("建表脚本缺 (venue,asset,taken_at) 唯一键 —— 重放会把同一瞬间重复计入")
	}
	// venue 必须在表里。这张表要是没这一列,它解决的就不是 #42 那个问题。
	if !ddlCols["venue"] {
		t.Error("建表脚本没有 venue 列")
	}
}

// createTableBody 抠出 CREATE TABLE <name> ( ... ) 的括号内文本。
func createTableBody(sql, table string) (string, bool) {
	i := strings.Index(sql, "CREATE TABLE IF NOT EXISTS "+table)
	if i < 0 {
		return "", false
	}
	open := strings.Index(sql[i:], "(")
	if open < 0 {
		return "", false
	}
	open += i
	depth := 0
	for j := open; j < len(sql); j++ {
		switch sql[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return sql[open+1 : j], true
			}
		}
	}
	return "", false
}

var ddlColLine = regexp.MustCompile(`^\s*([a-z_][a-z0-9_]*)\s+[A-Z]`)

// ddlColumnNames 取建表体里的列名:行首标识符 + 紧跟一个大写开头的类型。
// PRIMARY KEY / UNIQUE KEY / KEY 这些约束行首是大写,天然被过滤掉。
func ddlColumnNames(body string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(line); s == "" || strings.HasPrefix(s, "--") {
			continue
		}
		if m := ddlColLine.FindStringSubmatch(line); m != nil {
			out[m[1]] = true
		}
	}
	return out
}
