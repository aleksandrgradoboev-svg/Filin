package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Подпапки папки Филина (решение 15.11.2026): программа в корне, всё остальное разложено
// по смыслу. Библиотеку человек бережёт и переносит; остальное Филин умеет восстановить сам.
const (
	dirLibrary = "библиотека" // база, обложки, пробы распознавания
	dirCopies  = "копии"      // копии библиотеки перед обновлениями
	dirModels  = "модели"     // пакет распознавания: скачивается сам
	dirPhone   = "телефон"    // приложение для телефона
	dirLog     = "журнал"     // filin.log
)

// layout — где что лежит. Root — папка программы: там ярлычный значок и пометки.
type layout struct {
	Root, DB, Covers, Copies, Models, Phone, Log string
	// old — Филин разложен по-старому, всё в одной папке с программой: при запуске
	// программы (app) его надо разложить по подпапкам
	old bool
}

// flatLayout — всё в папке базы, как было до подпапок: база задана явно (-db), запуск из
// исходников или библиотека ещё не разложена.
func flatLayout(db string) layout {
	dir := filepath.Dir(db)
	return layout{Root: dir, DB: db, Covers: filepath.Join(dir, "covers"), Copies: dir, Models: dir,
		Phone: filepath.Join(dir, "app"), Log: dir}
}

// treeLayout — папка Филина с подпапками.
func treeLayout(root string) layout {
	lib := filepath.Join(root, dirLibrary)
	return layout{Root: root, DB: filepath.Join(lib, "filin.db"), Covers: filepath.Join(lib, "covers"),
		Copies: filepath.Join(root, dirCopies), Models: filepath.Join(root, dirModels),
		Phone: filepath.Join(root, dirPhone), Log: filepath.Join(root, dirLog)}
}

// chooseLayout — раскладка по тому, как запущен Филин. db — путь из -db (пусто — не задан).
func chooseLayout(db string) layout {
	if db != "" {
		return flatLayout(db)
	}
	exe, err := os.Executable()
	if err != nil {
		return flatLayout(filepath.Join("data", "filin.db"))
	}
	root := filepath.Dir(exe)
	if strings.Contains(strings.ToLower(root), "go-build") { // go run: база разработчика в data\
		return flatLayout(filepath.Join("data", "filin.db"))
	}
	return layoutIn(root)
}

// layoutIn — раскладка папки программы root.
func layoutIn(root string) layout {
	if exists(filepath.Join(root, "filin.db")) && !exists(filepath.Join(root, dirLibrary, "filin.db")) {
		l := flatLayout(filepath.Join(root, "filin.db"))
		l.old = true
		return l
	}
	// прежний умолчальный адрес до 0.5.1 — data\filin.db: такую библиотеку не подменять
	// новой пустой и не трогать
	if old := filepath.Join(root, "data", "filin.db"); exists(old) && !exists(filepath.Join(root, dirLibrary, "filin.db")) {
		return flatLayout(old)
	}
	return treeLayout(root)
}

// dirs — папки раскладки: создаются при запуске, чтобы никому не думать, есть ли они.
func (l layout) dirs() []string {
	return []string{filepath.Dir(l.DB), l.Copies, l.Models, l.Phone, l.Log}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// move — одно перемещение при раскладке.
type move struct{ from, to string }

// arrange раскладывает старую папку Филина root по подпапкам. Сначала копия базы в
// копии\, потом перемещения; не вышло одно — всё уже перемещённое возвращается назад, и
// Филин работает по-старому. Вызывать, только когда Филин не запущен и база закрыта.
func arrange(root string, now time.Time) (moved int, err error) {
	target := treeLayout(root)
	lib := filepath.Dir(target.DB)
	var plan []move
	add := func(name, toDir string) {
		plan = append(plan, move{filepath.Join(root, name), filepath.Join(toDir, name)})
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	for _, e := range ents {
		n := e.Name()
		switch {
		case n == "filin.db" || n == "filin.db-wal" || n == "filin.db-shm",
			n == "covers" && e.IsDir(), n == "ocr" && e.IsDir(),
			strings.HasPrefix(n, "до-восстановления-") && e.IsDir():
			add(n, lib)
		case strings.HasPrefix(n, copyPrefix) && strings.HasSuffix(n, ".zip"):
			add(n, target.Copies)
		case strings.HasPrefix(n, "ocr-engine-") && e.IsDir(), strings.HasPrefix(n, "filin-ocr-"):
			add(n, target.Models)
		case n == "filin.log" || n == "filin.log.1":
			add(n, target.Log)
		case n == "app" && e.IsDir():
			// содержимое app\ и есть папка «телефон»: переименовать целиком
			plan = append(plan, move{filepath.Join(root, n), target.Phone})
		}
	}
	for _, m := range plan {
		if exists(m.to) {
			return 0, fmt.Errorf("раскладка по папкам: %s уже есть — ничего не трогал", m.to)
		}
	}
	for _, d := range []string{lib, target.Copies, target.Models, target.Log} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return 0, err
		}
	}
	// копия базы до перемещений: перемещение не копирует, и спокойнее, когда база лежит ещё
	// и отдельно. Обложки только переименовываются — их копировать незачем
	safe := filepath.Join(target.Copies, "перед-раскладкой-"+now.Format("2006-01-02-150405"))
	if err := os.Mkdir(safe, 0o755); err != nil {
		return 0, err
	}
	for _, n := range []string{"filin.db", "filin.db-wal", "filin.db-shm"} {
		if err := copyFile(filepath.Join(root, n), filepath.Join(safe, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("копия базы перед раскладкой: %w", err)
		}
	}
	var done []move
	for _, m := range plan {
		if err := renameRetry(m.from, m.to); err != nil {
			var stuck []string
			for i := len(done) - 1; i >= 0; i-- {
				if rerr := renameRetry(done[i].to, done[i].from); rerr != nil {
					stuck = append(stuck, done[i].to)
				}
			}
			if len(stuck) > 0 { // копию базы не трогаем: она сейчас нужнее всего
				return 0, fmt.Errorf("раскладка по папкам: %s: %v; вернуть на место не удалось: %s — данные целы, копия базы в %s",
					filepath.Base(m.from), err, strings.Join(stuck, ", "), safe)
			}
			os.RemoveAll(safe) // всё вернулось — копия не нужна, иначе копилась бы на каждом запуске
			return 0, fmt.Errorf("раскладка по папкам: %s: %w — всё возвращено как было", filepath.Base(m.from), err)
		}
		done = append(done, m)
	}
	return len(done), nil
}

// renameRetries — сколько раз пробовать переместить занятый файл. Сразу после обновления
// прежний Филин ещё выходит и держит открытым журнал: подождать несколько секунд.
var renameRetries, renameWait = 20, 250 * time.Millisecond

func renameRetry(from, to string) error {
	var err error
	for i := 0; i < renameRetries; i++ {
		if err = os.Rename(from, to); err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(renameWait)
	}
	return err
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
