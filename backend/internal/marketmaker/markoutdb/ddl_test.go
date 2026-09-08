package markoutdb

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"quanty_trade/internal/models"
)

// TestDDLMatchesModel 把手写的建表脚本和 GORM 模型钉在一起。
//
// 为什么值得一条测试:markout_fills 不在 AutoMigrate 名单里(建表是所有者的事),
// 所以【没有任何运行时机制】会发现 DDL 和模型对不上 —— 表照建、程序照跑,
// 只是每次 INSERT 报 unknown column,而那是在生产上才看得到的。
//
// 这个坑本轮已经踩过一次:GORM 默认命名把 Mid1s 写成 "mid1s"、Lag1sMs 写成
// "lag1s_ms"(数字前不加下划线),与脚本里的 mid_1s/lag_1s_ms 差一个下划线。
// 模型侧已用显式 column: 钉死,这条测试保证两边不会再分家。
func TestDDLMatchesModel(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "scripts", "markout_persistence.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到建表脚本 %s: %v", path, err)
	}

	body, ok := createTableBody(string(raw), "markout_fills")
	if !ok {
		t.Fatal("脚本里找不到 CREATE TABLE markout_fills(...)")
	}
	ddlCols := ddlColumnNames(body)

	db := testDB(t)
	types, err := db.Migrator().ColumnTypes(&models.MarkoutFill{})
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

	// 唯一键决定"重复写是 no-op 还是重复计数",漏了它整张表的统计都会偏。
	if !strings.Contains(body, "UNIQUE KEY idx_markout_fill_uniq (exchange, symbol, fill_id)") {
		t.Error("建表脚本缺 (exchange,symbol,fill_id) 唯一键 —— 引擎重拉成交会把同一笔重复计入")
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
