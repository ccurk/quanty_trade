package database

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// freshDB 每个用例一个独立的库文件。不用 file::memory:?cache=shared——
// 那会让同进程的用例共享同一份内存库,"空库"用例会被别的用例建的表污染。
func freshDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "verify.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func mustExec(t *testing.T, db *gorm.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

// 全新安装:库里一张表都没有,必须放行,否则第一次部署就起不来。
func TestVerifyOwnSchema_EmptyDBPasses(t *testing.T) {
	if err := verifyOwnSchema(freshDB(t)); err != nil {
		t.Fatalf("空库应放行(全新安装),却报错: %v", err)
	}
}

// 连对了:库里有本业务的签名表 → 放行。逐张签名表单独验一遍,
// 保证任意一张在就够,不要求三张全在。
func TestVerifyOwnSchema_OwnDBPasses(t *testing.T) {
	for _, sig := range signatureTables {
		t.Run(sig, func(t *testing.T) {
			db := freshDB(t)
			// 混入一张无关表,确认判定看的是"有没有签名表"而不是"只有签名表"。
			mustExec(t, db,
				"CREATE TABLE "+sig+" (id INTEGER PRIMARY KEY)",
				"CREATE TABLE some_unrelated_cache (id INTEGER PRIMARY KEY)",
			)
			if err := verifyOwnSchema(db); err != nil {
				t.Fatalf("库里有 %s,应判为本业务的库,却报错: %v", sig, err)
			}
		})
	}
}

// 连错了:库里有表、但没有任何签名表 → 必须报错。
// 表名取自 3307 上 tg-jobs-mysql 的 tg_jobs 库实测清单,
// 这正是配置写成 3307 时会连上去的那个库。
func TestVerifyOwnSchema_ForeignDBFails(t *testing.T) {
	db := freshDB(t)
	mustExec(t, db,
		"CREATE TABLE jobs (id INTEGER PRIMARY KEY)",
		"CREATE TABLE talents (id INTEGER PRIMARY KEY)",
		"CREATE TABLE billing_orders (id INTEGER PRIMARY KEY)",
		// users 是两边都可能有的通用表名,不能靠它认亲,所以放进来当反例。
		"CREATE TABLE users (id INTEGER PRIMARY KEY)",
	)
	err := verifyOwnSchema(db)
	if err == nil {
		t.Fatal("连到陌生库(tg_jobs)必须报错,却放行了——AutoMigrate 会把本业务的表建进别人的生产库")
	}
	// 报错信息要能让人当场看出连到哪儿了,否则运维还得自己猜。
	for _, want := range []string{"jobs", "strategy_templates"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错信息应含 %q,实际: %v", want, err)
		}
	}
}
