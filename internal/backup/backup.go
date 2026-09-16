// Package backup: sao lưu SQLite hằng ngày (VACUUM INTO + integrity_check + prune) và khôi phục
// qua file staging `<dsn>.restore-pending` được áp lúc khởi động, trước khi mở DB (spec §4).
package backup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const stamp = "20060102-150405"

var (
	// NameRe: tên bản sao lưu hợp lệ (chống path traversal ở API tải/khôi phục).
	NameRe   = regexp.MustCompile(`^(smsie|pre-restore)-\d{8}-\d{6}\.db$`)
	pruneRe  = regexp.MustCompile(`^smsie-\d{8}-\d{6}\.db$`)
	sqlMagic = []byte("SQLite format 3\x00")
)

type Result struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	SizeBytes int64    `json:"size_bytes"`
	Pruned    []string `json:"pruned"`
}

type Info struct {
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	ModTime   time.Time `json:"mod_time"`
}

// Integrity: header `SQLite format 3\0` + PRAGMA integrity_check phải trả đúng một dòng "ok".
func Integrity(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	head := make([]byte, len(sqlMagic))
	_, rerr := io.ReadFull(f, head)
	f.Close()
	if rerr != nil || !bytes.Equal(head, sqlMagic) {
		return errors.New("không phải file SQLite")
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return err
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	var rows []string
	if err := db.Raw("PRAGMA integrity_check").Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "ok" {
		return fmt.Errorf("integrity_check: %s", strings.Join(rows, "; "))
	}
	return nil
}

// Run: VACUUM INTO file tạm trong dir → Integrity → đổi tên thành smsie-YYYYMMDD-HHMMSS.db → xoá
// bản cũ ngoài keep (chỉ đụng file khớp pruneRe; pre-restore-* giữ nguyên).
func Run(db *gorm.DB, dir string, keep int) (Result, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	name := "smsie-" + time.Now().Format(stamp) + ".db"
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"
	os.Remove(tmp)
	if err := db.Exec("VACUUM INTO '" + strings.ReplaceAll(tmp, "'", "''") + "'").Error; err != nil {
		return Result{}, err
	}
	if err := Integrity(tmp); err != nil {
		os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Rename(tmp, final); err != nil { // ponytail: 2 lần chạy trong cùng giây → lỗi, không ghi đè
		os.Remove(tmp)
		return Result{}, err
	}
	st, err := os.Stat(final)
	if err != nil {
		return Result{}, err
	}
	res := Result{Path: final, Name: name, SizeBytes: st.Size(), Pruned: []string{}}
	if keep > 0 {
		var olds []string
		for _, it := range List(dir) { // mới nhất trước
			if pruneRe.MatchString(it.Name) {
				olds = append(olds, it.Name)
			}
		}
		for i := keep; i < len(olds); i++ {
			if err := os.Remove(filepath.Join(dir, olds[i])); err == nil {
				res.Pruned = append(res.Pruned, olds[i])
			}
		}
	}
	return res, nil
}

// List: các bản trong dir khớp NameRe, tên giảm dần (tên mang timestamp nên = mới nhất trước).
func List(dir string) []Info {
	out := []Info{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if e.IsDir() || !NameRe.MatchString(e.Name()) {
			continue
		}
		if st, err := e.Info(); err == nil {
			out = append(out, Info{Name: e.Name(), SizeBytes: st.Size(), ModTime: st.ModTime()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

// StagePath: file chờ khôi phục, cạnh DB thật.
func StagePath(dsn string) string { return dsn + ".restore-pending" }

// ApplyPending: gọi TRƯỚC khi mở DB. Có staging → kiểm integrity (hỏng → xoá staging, trả lỗi),
// chép DB hiện tại thành dir/pre-restore-<stamp>.db, rồi đổi tên staging → dsn.
func ApplyPending(dsn, dir string) (bool, error) {
	stage := StagePath(dsn)
	if _, err := os.Stat(stage); err != nil {
		return false, nil
	}
	if err := Integrity(stage); err != nil {
		os.Remove(stage)
		return false, fmt.Errorf("file khôi phục hỏng, đã bỏ: %w", err)
	}
	if _, err := os.Stat(dsn); err == nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		if err := snapshot(dsn, filepath.Join(dir, "pre-restore-"+time.Now().Format(stamp)+".db")); err != nil {
			return false, err
		}
	}
	// WAL/journal cũ thuộc DB cũ (đã gom vào pre-restore ở trên); xoá TRƯỚC khi đổi tên, nếu
	// không crash giữa chừng sẽ để chúng áp nhầm lên DB mới.
	for _, suf := range []string{"-wal", "-shm", "-journal"} {
		os.Remove(dsn + suf)
	}
	if err := os.Rename(stage, dsn); err != nil {
		return false, err
	}
	return true, nil
}

// snapshot: mở DB cũ bằng driver sqlite rồi VACUUM INTO — gom cả WAL/hot journal chưa checkpoint
// (os.Exit(3) sau restore không đóng DB êm). DB cũ hỏng tới mức không mở được → chép thô.
func snapshot(src, dst string) error {
	db, err := gorm.Open(sqlite.Open(src), &gorm.Config{Logger: gormlogger.Discard})
	if err == nil {
		err = db.Exec("VACUUM INTO '" + strings.ReplaceAll(dst, "'", "''") + "'").Error
		if sqlDB, e := db.DB(); e == nil {
			sqlDB.Close()
		}
		if err == nil {
			return nil
		}
		os.Remove(dst)
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Scheduler chạy Run mỗi ngày lúc Hour:00 (không chạy lúc boot), ghi audit backup.ok/backup.failed,
// lỗi thì Notify (webhook tới mọi kênh).
type Scheduler struct {
	DB     *gorm.DB
	Dir    string
	Keep   int
	Hour   int
	Notify func(text string)
}

func (s *Scheduler) Run(stop <-chan struct{}) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		select {
		case <-stop:
			return
		case <-time.After(time.Until(next)):
		}
		s.Once()
	}
}

// Once: một lượt sao lưu + ghi sổ (tách ra để main/test gọi thẳng).
func (s *Scheduler) Once() (Result, error) {
	res, err := Run(s.DB, s.Dir, s.Keep)
	if err != nil {
		logger.Log.Errorf("backup: %v", err)
		audit.Record(s.DB, audit.Entry{Username: "system", Action: "backup.failed", Detail: err.Error(), Status: 500})
		if s.Notify != nil {
			s.Notify("Sao lưu tự động THẤT BẠI: " + err.Error())
		}
		return res, err
	}
	logger.Log.Infof("backup: %s (%d bytes, xoá %d bản cũ)", res.Name, res.SizeBytes, len(res.Pruned))
	audit.Record(s.DB, audit.Entry{Username: "system", Action: "backup.ok", Target: res.Name, Detail: fmt.Sprintf(`{"name":%q,"size_bytes":%d,"pruned":%d}`, res.Name, res.SizeBytes, len(res.Pruned)), Status: 200})
	return res, nil
}
