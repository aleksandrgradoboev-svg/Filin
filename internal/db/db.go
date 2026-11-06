// Package db открывает базу SQLite и накатывает схему.
//
// Номер схемы хранится в PRAGMA user_version: миграция N применяется, если версия базы меньше N.
// Каждая миграция — отдельная транзакция; упала — база остаётся на прежней версии.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open открывает базу по пути к файлу и приводит схему к последней версии.
// Прагмы передаются в DSN, чтобы действовать на каждом соединении пула, а не на одном.
// Журнал WAL сюда не входит — см. enableWAL.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range []string{"busy_timeout(5000)", "foreign_keys(1)"} {
		q.Add("_pragma", p)
	}
	d, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := Migrate(d); err != nil {
		d.Close()
		return nil, err
	}
	if err := enableWAL(d); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func newerErr(cur, latest int) error {
	return fmt.Errorf("схема базы %d новее этой сборки (%d) — нужен свежий filin", cur, latest)
}

// enableWAL переключает журнал в WAL. Режим хранится в файле базы, так что переключение
// нужно один раз, а дальше прагма ничего не делает. Смена журнала не ждёт по busy_timeout:
// когда два процесса разом открывают свежую базу, она сразу отвечает SQLITE_BUSY
// (замер 29.10.2026: 23 отказа из 40 открытий). Поэтому повтор.
func enableWAL(d *sql.DB) error {
	for try := 0; ; try++ {
		var mode string
		err := d.QueryRow("PRAGMA journal_mode = WAL").Scan(&mode)
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_BUSY && try < 50 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err != nil {
			return fmt.Errorf("журнал WAL: %w", err)
		}
		if mode != "wal" {
			return fmt.Errorf("журнал WAL не включился, режим %q", mode)
		}
		return nil
	}
}

// Version — текущая версия схемы базы.
func Version(d *sql.DB) (int, error) {
	var v int
	err := d.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// Migrate применяет недостающие миграции по порядку номеров.
// База новее сборки — ошибка: старый бинарь не знает, что значат новые таблицы и поля.
func Migrate(d *sql.DB) error {
	list, err := load()
	if err != nil {
		return err
	}
	// Актуальная база — обычный случай: проверяется чтением, без блокировки записи,
	// чтобы открытие не ждало чужой длинной записи.
	cur, err := Version(d)
	if err != nil {
		return err
	}
	if cur > len(list) {
		return newerErr(cur, len(list))
	}
	if cur == len(list) {
		return nil
	}
	// Одно соединение и BEGIN IMMEDIATE: версия перечитывается под блокировкой записи,
	// поэтому два процесса, открывшие пустую базу разом, не применят миграцию дважды.
	ctx := context.Background()
	conn, err := d.Conn(ctx)
	if err != nil {
		return fmt.Errorf("подключение: %w", err)
	}
	defer conn.Close()
	for _, m := range list {
		if err := apply(ctx, conn, m, len(list)); err != nil {
			return err
		}
	}
	return nil
}

func apply(ctx context.Context, conn *sql.Conn, m migration, latest int) (err error) {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("блокировка для миграции: %w", err)
	}
	defer func() {
		if err != nil {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	var cur int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
		return err
	}
	if cur > latest {
		return newerErr(cur, latest)
	}
	if m.n <= cur {
		_, err := conn.ExecContext(ctx, "COMMIT")
		return err
	}
	if _, err := conn.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("миграция %s: %w", m.name, err)
	}
	// PRAGMA не принимает параметры — номер подставляется числом.
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.n)); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

type migration struct {
	n    int
	name string
	sql  string
}

// load читает миграции «NNNN_имя.sql» и проверяет, что номера идут подряд с 1:
// пропуск номера — почти всегда опечатка в имени файла, а не замысел.
func load() ([]migration, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var list []migration
	for _, p := range names {
		name := strings.TrimPrefix(p, "migrations/")
		num, _, ok := strings.Cut(name, "_")
		n, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("миграция %s: имя не по образцу NNNN_имя.sql", name)
		}
		body, err := migrations.ReadFile(p)
		if err != nil {
			return nil, err
		}
		list = append(list, migration{n: n, name: name, sql: string(body)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n < list[j].n })
	for i, m := range list {
		if m.n != i+1 {
			return nil, fmt.Errorf("миграция %s: ждали номер %d", m.name, i+1)
		}
	}
	return list, nil
}

// Latest — номер последней миграции, известной этой сборке.
func Latest() (int, error) {
	list, err := load()
	return len(list), err
}
