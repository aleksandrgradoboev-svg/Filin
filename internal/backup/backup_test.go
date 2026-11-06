package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

// library — база с одной книгой и одна обложка, как у живого Филина.
func library(t *testing.T, title string) (dir, dbPath, covers string) {
	t.Helper()
	dir = t.TempDir()
	dbPath, covers = filepath.Join(dir, "filin.db"), filepath.Join(dir, "covers")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := catalog.New(d).AddEdition(&catalog.Edition{Title: title}); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(covers, 0o755)
	os.WriteFile(filepath.Join(covers, "ab12.jpg"), []byte("фото "+title), 0o644)
	return
}

func titles(t *testing.T, dbPath string) []string {
	t.Helper()
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query(`SELECT title FROM edition ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// Копия, снятая с работающей базы, восстанавливается в другую библиотеку целиком,
// а прежние данные той библиотеки откладываются, а не стираются.
func TestWriteRestore(t *testing.T) {
	_, srcDB, srcCovers := library(t, "Остров сокровищ")
	d, err := db.Open(srcDB) // база открыта и работает, пока снимается копия
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Write(context.Background(), &buf, d, srcCovers, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	d.Close()
	archive := filepath.Join(t.TempDir(), FileName(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)))
	os.WriteFile(archive, buf.Bytes(), 0o644)
	if filepath.Base(archive) != "filin-2026-11-06.zip" {
		t.Errorf("имя архива: %s", archive)
	}

	dir, dstDB, dstCovers := library(t, "Чужая книга")
	aside, err := Restore(archive, dstDB, dstCovers, time.Date(2026, 11, 6, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(t, dstDB); len(got) != 1 || got[0] != "Остров сокровищ" {
		t.Errorf("после восстановления: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dstCovers, "ab12.jpg")); string(b) != "фото Остров сокровищ" {
		t.Errorf("обложка: %q", b)
	}
	if aside != filepath.Join(dir, "до-восстановления-2026-11-06-123000") {
		t.Errorf("отложено в %s", aside)
	}
	if got := titles(t, filepath.Join(aside, "filin.db")); len(got) != 1 || got[0] != "Чужая книга" {
		t.Errorf("прежняя база не отложена: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(aside, "covers", "ab12.jpg")); string(b) != "фото Чужая книга" {
		t.Errorf("прежние обложки не отложены: %q", b)
	}
}

func zipOf(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	f, _ := os.Create(p)
	z := zip.NewWriter(f)
	for n, body := range files {
		w, _ := z.Create(n)
		w.Write([]byte(body))
	}
	z.Close()
	f.Close()
	return p
}

// Чужой или битый архив не трогает библиотеку.
func TestRestoreRefuses(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"без базы":         {"covers/a.jpg": "x"},
		"выход из папки":   {"filin.db": "x", "covers/../../evil.txt": "x"},
		"лишний файл":      {"filin.db": "x", "readme.txt": "x"},
		"база — не SQLite": {"filin.db": "это не база"},
		"обложка с путём":  {"filin.db": "x", `covers/..\evil.jpg`: "x"},
	} {
		dir, dbPath, covers := library(t, "Моя книга")
		if _, err := Restore(zipOf(t, files), dbPath, covers, time.Now()); err == nil {
			t.Errorf("%s: архив принят", name)
		}
		if got := titles(t, dbPath); len(got) != 1 || got[0] != "Моя книга" {
			t.Errorf("%s: библиотека задета: %v", name, got)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "до-восстановления") || strings.HasPrefix(e.Name(), "восстановление-") {
				t.Errorf("%s: остался %s", name, e.Name())
			}
		}
	}
}

// Сбой на полпути — библиотека возвращается как была. На Windows папку с открытым файлом
// не переименовать: так и бывает, когда фото открыто в просмотрщике.
func TestRestoreRollsBack(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("открытый файл мешает переименованию только на Windows")
	}
	_, srcDB, srcCovers := library(t, "Из архива")
	d, _ := db.Open(srcDB)
	var buf bytes.Buffer
	if err := Write(context.Background(), &buf, d, srcCovers, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	d.Close()
	archive := filepath.Join(t.TempDir(), "a.zip")
	os.WriteFile(archive, buf.Bytes(), 0o644)

	dir, dbPath, covers := library(t, "Моя книга")
	busy, err := os.Open(filepath.Join(covers, "ab12.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if _, err := Restore(archive, dbPath, covers, time.Now()); err == nil {
		t.Fatal("восстановление прошло при занятой папке обложек")
	}
	if got := titles(t, dbPath); len(got) != 1 || got[0] != "Моя книга" {
		t.Errorf("база не вернулась на место: %v", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "до-восстановления") {
			t.Errorf("осталась папка %s", e.Name())
		}
	}
}
