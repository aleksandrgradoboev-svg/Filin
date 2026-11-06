package db

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filin.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := Latest()
	if err != nil || latest == 0 {
		t.Fatalf("миграции не загрузились: %d, %v", latest, err)
	}
	v, err := Version(d)
	if err != nil || v != latest {
		t.Fatalf("версия %d, ждали %d (%v)", v, latest, err)
	}
	d.Close()

	// повторное открытие не должно пытаться создать таблицы второй раз
	d, err = Open(path)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer d.Close()

	var fk int
	if err := d.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d (%v)", fk, err)
	}
	var mode string
	if err := d.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q (%v)", mode, err)
	}
}

// Два процесса открывают пустую базу разом — оба должны открыть её без ошибки.
func TestConcurrentOpenOfEmptyBase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filin.db")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := Open(path)
			if err == nil {
				d.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("открытие %d: %v", i, err)
		}
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filin.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "новее") {
		t.Fatalf("база новее сборки должна отклоняться, получено %v", err)
	}
}
