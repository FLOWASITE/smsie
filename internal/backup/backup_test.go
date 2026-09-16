package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func openDB(t *testing.T, path, table string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY, v TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO " + table + " (v) VALUES ('x')")
	return db
}

func closeDB(db *gorm.DB) { s, _ := db.DB(); s.Close() }

func TestRunCreatesValidBackupAndPrunes(t *testing.T) {
	tmp := t.TempDir()
	db := openDB(t, filepath.Join(tmp, "live.db"), "a")
	defer closeDB(db)
	dir := filepath.Join(tmp, "backups")
	os.MkdirAll(dir, 0o755)
	for _, old := range []string{"smsie-20200101-000000.db", "smsie-20200102-000000.db", "pre-restore-20200101-000000.db", "khac.db"} {
		os.WriteFile(filepath.Join(dir, old), []byte("junk"), 0o644)
	}
	res, err := Run(db, dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !NameRe.MatchString(res.Name) || res.SizeBytes == 0 || len(res.Pruned) != 1 || res.Pruned[0] != "smsie-20200101-000000.db" {
		t.Fatalf("res = %+v", res)
	}
	if err := Integrity(res.Path); err != nil {
		t.Fatalf("bản sao lưu phải hợp lệ: %v", err)
	}
	names := []string{}
	for _, it := range List(dir) {
		names = append(names, it.Name)
	}
	if len(names) != 3 || names[0] != res.Name || names[1] != "smsie-20200102-000000.db" || names[2] != "pre-restore-20200101-000000.db" {
		t.Fatalf("list = %v", names)
	}
	if _, err := os.Stat(filepath.Join(dir, "khac.db")); err != nil {
		t.Fatal("file ngoài mẫu không được xoá")
	}
}

func TestIntegrityRejectsGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rac.db")
	os.WriteFile(p, []byte("SQLite format 3\x00 nhưng phần sau là rác"), 0o644)
	if err := Integrity(p); err == nil {
		t.Fatal("file rác phải bị từ chối")
	}
	os.WriteFile(p, []byte("hello"), 0o644)
	if err := Integrity(p); err == nil {
		t.Fatal("thiếu header phải bị từ chối")
	}
	if err := Integrity(filepath.Join(t.TempDir(), "khong-co.db")); err == nil {
		t.Fatal("file không tồn tại phải lỗi")
	}
}

func TestApplyPending(t *testing.T) {
	tmp := t.TempDir()
	dsn := filepath.Join(tmp, "live.db")
	dir := filepath.Join(tmp, "backups")
	if ok, err := ApplyPending(dsn, dir); ok || err != nil {
		t.Fatalf("không có staging: %v %v", ok, err)
	}
	closeDB(openDB(t, dsn, "cu"))
	closeDB(openDB(t, StagePath(dsn), "moi"))
	ok, err := ApplyPending(dsn, dir)
	if !ok || err != nil {
		t.Fatalf("apply: %v %v", ok, err)
	}
	if _, err := os.Stat(StagePath(dsn)); err == nil {
		t.Fatal("staging phải biến mất")
	}
	db, _ := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	var n int64
	if db.Raw("SELECT COUNT(*) FROM moi").Scan(&n); n != 1 {
		t.Fatal("dsn phải là DB mới")
	}
	closeDB(db)
	pre := List(dir)
	if len(pre) != 1 || pre[0].Name[:12] != "pre-restore-" {
		t.Fatalf("thiếu pre-restore: %+v", pre)
	}
	db, _ = gorm.Open(sqlite.Open(filepath.Join(dir, pre[0].Name)), &gorm.Config{Logger: gormlogger.Discard})
	if db.Raw("SELECT COUNT(*) FROM cu").Scan(&n); n != 1 {
		t.Fatal("pre-restore phải là DB cũ")
	}
	closeDB(db)

	os.WriteFile(StagePath(dsn), []byte("rac"), 0o644)
	if ok, err := ApplyPending(dsn, dir); ok || err == nil {
		t.Fatalf("staging rác phải lỗi: %v %v", ok, err)
	}
	if _, err := os.Stat(StagePath(dsn)); err == nil {
		t.Fatal("staging rác phải bị xoá")
	}
	db, _ = gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if db.Raw("SELECT COUNT(*) FROM moi").Scan(&n); n != 1 {
		t.Fatal("dsn không được đụng khi staging rác")
	}
	closeDB(db)
}
