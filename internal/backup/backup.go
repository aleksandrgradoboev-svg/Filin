// Package backup — резервная копия библиотеки одним архивом и восстановление из него.
//
// В архиве всё, что нельзя восстановить: база (filin.db) и папка обложек и фото (covers/).
// Где хранить архив, решает человек: Филин его только отдаёт.
package backup

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

const (
	dbName    = "filin.db"
	coversDir = "covers"
)

// FileName — имя архива для скачивания: filin-2026-11-06.zip.
func FileName(now time.Time) string { return "filin-" + now.Format("2006-01-02") + ".zip" }

// Write пишет архив: снимок базы (VACUUM INTO — целостный, даже пока Филин работает и
// пишет) и обложки. tmpDir — где положить снимок на время упаковки.
func Write(ctx context.Context, w io.Writer, d *sql.DB, covers, tmpDir string) error {
	dir, err := os.MkdirTemp(tmpDir, "filin-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	snap := filepath.Join(dir, dbName)
	if _, err := d.ExecContext(ctx, `VACUUM INTO ?`, snap); err != nil {
		return fmt.Errorf("снимок базы: %w", err)
	}
	z := zip.NewWriter(w)
	if err := addFile(z, snap, dbName); err != nil {
		return err
	}
	entries, err := os.ReadDir(covers)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := addFile(z, filepath.Join(covers, e.Name()), coversDir+"/"+e.Name()); err != nil {
			return err
		}
	}
	return z.Close()
}

func addFile(z *zip.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	h, err := zip.FileInfoHeader(st)
	if err != nil {
		return err
	}
	h.Name, h.Method = name, zip.Deflate
	if strings.HasSuffix(strings.ToLower(name), ".jpg") || strings.HasSuffix(strings.ToLower(name), ".png") {
		h.Method = zip.Store // фото уже сжаты — жать их второй раз только тратить время
	}
	out, err := z.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, f)
	return err
}

// Restore заменяет базу dbPath и обложки covers содержимым архива. Нынешние данные не
// стираются, а откладываются в папку «до-восстановления-<время>» рядом с базой; её имя
// возвращается. Филин в это время должен быть закрыт.
func Restore(archive, dbPath, covers string, now time.Time) (string, error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return "", fmt.Errorf("это не архив Филина: %w", err)
	}
	defer z.Close()

	var dbFile *zip.File
	var coverFiles []*zip.File
	for _, f := range z.File {
		switch name := path.Clean(f.Name); {
		case name == dbName:
			dbFile = f
		case path.Dir(name) == coversDir && validName(path.Base(name)):
			coverFiles = append(coverFiles, f)
		case f.FileInfo().IsDir():
		default:
			return "", fmt.Errorf("в архиве лишний файл %q — это не архив Филина", f.Name)
		}
	}
	if dbFile == nil {
		return "", fmt.Errorf("в архиве нет %s — это не архив Филина", dbName)
	}

	// сначала распаковать во временное место рядом и проверить базу, потом менять местами:
	// битый архив не должен оставить человека без библиотеки
	base := filepath.Dir(dbPath)
	stage, err := os.MkdirTemp(base, "восстановление-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := extract(dbFile, filepath.Join(stage, dbName)); err != nil {
		return "", err
	}
	if err := check(filepath.Join(stage, dbName)); err != nil {
		return "", err
	}
	if err := os.Mkdir(filepath.Join(stage, coversDir), 0o755); err != nil {
		return "", err
	}
	for _, f := range coverFiles {
		if err := extract(f, filepath.Join(stage, coversDir, path.Base(path.Clean(f.Name)))); err != nil {
			return "", err
		}
	}

	aside := filepath.Join(base, "до-восстановления-"+now.Format("2006-01-02-150405"))
	if err := os.Mkdir(aside, 0o755); err != nil {
		return "", err
	}
	// всё, что сдвинуто, записывается: сбой на полпути (Филин всё же открыт, фото открыто
	// в просмотрщике) возвращает библиотеку как была, а не оставляет её без базы
	type move struct{ from, to string }
	var done []move
	rollback := func(cause error) (string, error) {
		for i := len(done) - 1; i >= 0; i-- {
			if err := os.Rename(done[i].to, done[i].from); err != nil {
				return aside, fmt.Errorf("%w; вернуть %s на место не удалось (%v) — он в %s", cause, done[i].from, err, aside)
			}
		}
		os.Remove(aside)
		return "", cause
	}
	step := func(from, to string) error {
		if err := os.Rename(from, to); err != nil {
			return err
		}
		done = append(done, move{from, to})
		return nil
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", covers} {
		if err := step(p, filepath.Join(aside, filepath.Base(p))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return rollback(fmt.Errorf("не удалось отложить %s (Филин закрыт?): %w", p, err))
		}
	}
	if err := step(filepath.Join(stage, dbName), dbPath); err != nil {
		return rollback(err)
	}
	if err := step(filepath.Join(stage, coversDir), covers); err != nil {
		return rollback(err)
	}
	return aside, nil
}

// validName — простое имя файла без путей: обложки называются хэшем с расширением.
func validName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, `/\:`)
}

const maxEntry = 4 << 30

func extract(f *zip.File, dst string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	// предел на файл: «архив-бомба» из чужих рук не должна забить диск. 4 ГБ — с запасом
	// на базу в тысячи книг; фото уменьшены до 1200 px и весят сотни килобайт
	n, err := io.Copy(out, io.LimitReader(r, maxEntry+1))
	if err == nil && n > maxEntry {
		err = fmt.Errorf("%s в архиве больше %d ГБ — это не копия Филина", f.Name, maxEntry>>30)
	}
	if err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// check открывает базу из архива: SQLite ли это и знакома ли схема этой версии Филина.
func check(p string) error {
	d, err := db.Open(p)
	if err != nil {
		return fmt.Errorf("база в архиве не открывается: %w", err)
	}
	return d.Close()
}
