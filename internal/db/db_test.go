package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
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

// Миграция 0010 (шкала alib, 1–6) меняет колонку grade у book_copy без пересборки таблицы:
// оценки 1–5 не сдвигаются, выдачи и оценки в рублях — на месте, 6 принимается, 7 — нет.
func TestAlibGradesMigrationKeepsData(t *testing.T) {
	q := url.Values{}
	for _, p := range []string{"busy_timeout(5000)", "foreign_keys(1)"} {
		q.Add("_pragma", p)
	}
	d, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "filin.db")+"?"+q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	list, err := load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := d.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list[:9] { // база, какой она была до шкалы alib
		if err := apply(ctx, conn, m, len(list)); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	for _, s := range []string{
		`INSERT INTO edition (id, title) VALUES (1, 'Оверлорд')`,
		`INSERT INTO book_copy (id, edition_id, grade, notes) VALUES (1, 1, 1, 'a'), (2, 1, 2, ''), (3, 1, 3, ''),
			(4, 1, 4, ''), (5, 1, 5, 'отличная'), (6, 1, NULL, '')`,
		`INSERT INTO loan (copy_id, borrower, lent_on) VALUES (5, 'Петя', '2026-11-01')`,
		`INSERT INTO valuation (copy_id, amount, amount_to, valued_on) VALUES (5, 200, 790, '2026-11-08')`,
	} {
		if _, err := d.Exec(s); err != nil {
			t.Fatal(err)
		}
	}

	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	if v, _ := Version(d); v != len(list) {
		t.Fatalf("версия %d", v)
	}
	rows, err := d.Query(`SELECT id, COALESCE(grade, 0), notes FROM book_copy ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var id, g int
		var n string
		rows.Scan(&id, &g, &n)
		got = append(got, fmt.Sprintf("%d:%d:%s", id, g, n))
	}
	rows.Close()
	if strings.Join(got, " ") != "1:1:a 2:2: 3:3: 4:4: 5:5:отличная 6:0:" {
		t.Errorf("оценки после миграции: %v", got)
	}
	var loans, vals int
	d.QueryRow(`SELECT COUNT(*) FROM loan`).Scan(&loans)
	d.QueryRow(`SELECT COUNT(*) FROM valuation`).Scan(&vals)
	if loans != 1 || vals != 1 {
		t.Errorf("выдач %d, оценок %d — каскад задел данные", loans, vals)
	}
	if _, err := d.Exec(`UPDATE book_copy SET grade = 6 WHERE id = 6`); err != nil {
		t.Errorf("«идеальное» (6) не принято: %v", err)
	}
	if _, err := d.Exec(`UPDATE book_copy SET grade = 7 WHERE id = 6`); err == nil {
		t.Error("оценка 7 принята")
	}
	var ok string
	if err := d.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Errorf("integrity_check: %q %v", ok, err)
	}
	if r, err := d.Query(`PRAGMA foreign_key_check`); err != nil || r.Next() {
		t.Error("нарушены внешние ключи")
		if r != nil {
			r.Close()
		}
	} else {
		r.Close()
	}
}
