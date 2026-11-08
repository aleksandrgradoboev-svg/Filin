// Package update — самообновление программы на компьютере из выпусков GitHub.
//
// Порядок: Latest узнаёт последний выпуск, Newer решает, новее ли он запущенного,
// Download скачивает filin.exe рядом с работающим под именем .new и сверяет SHA-256 с
// суммой, которую GitHub отдаёт у каждого файла выпуска, Swap переставляет файлы.
// Работающий exe Windows перезаписать не даёт, а переименовать — даёт: старый уходит в
// .old, новый встаёт на его место, а .old убирает Cleanup при следующем запуске.
//
// Сумма приходит из того же источника, что и файл: она ловит битую загрузку, но не
// подмену выпуска на самом GitHub.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// LatestURL — последний выпуск Филина: черновики и предварительные выпуски GitHub сюда не отдаёт.
const LatestURL = "https://api.github.com/repos/aleksandrgradoboev-svg/Filin/releases/latest"

// Asset — имя программы среди файлов выпуска.
const Asset = "filin.exe"

// maxSize — больше программа не бывает: выпуск 0.6.0 весит 31 МБ вместе с приложением для
// телефона. Предел защищает диск от ответа, который не кончается.
const maxSize = 300 << 20

// Release — выпуск, к которому можно обновиться.
type Release struct {
	Version string // тег: v0.6.1
	URL     string // откуда скачивать filin.exe
	SHA256  string // сумма файла, шестнадцатеричная, строчными
	Size    int64
	Page    string // страница выпуска — показать человеку, что нового
}

// Latest спрашивает последний выпуск. ua — подпись программы: GitHub без неё отказывает.
func Latest(ctx context.Context, c *http.Client, url, ua string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("сервер выпусков ответил %s", resp.Status)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		Page   string `json:"html_url"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("ответ о выпуске не разобран: %w", err)
	}
	if _, ok := parse(r.Tag); !ok {
		return Release{}, fmt.Errorf("у выпуска непонятная версия %q", r.Tag)
	}
	for _, a := range r.Assets {
		if a.Name != Asset {
			continue
		}
		sum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || len(sum) != sha256.Size*2 {
			// без суммы не обновляемся: проверить, что скачалось целиком, будет нечем
			return Release{}, fmt.Errorf("у %s в выпуске %s нет суммы SHA-256", Asset, r.Tag)
		}
		if a.Size <= 0 || a.Size > maxSize {
			return Release{}, fmt.Errorf("у %s в выпуске %s странный размер: %d", Asset, r.Tag, a.Size)
		}
		return Release{Version: r.Tag, URL: a.URL, SHA256: strings.ToLower(sum), Size: a.Size, Page: r.Page}, nil
	}
	return Release{}, fmt.Errorf("в выпуске %s нет %s", r.Tag, Asset)
}

// parse разбирает vX.Y.Z. Другие версии (dev, v1.2, v1.2.3-rc1) не годятся: сборку
// разработчика не обновляем, предварительных выпусков не ставим.
func parse(v string) ([3]int, bool) {
	var n [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || p != strconv.Itoa(x) {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// IsRelease — версия выпуска (vX.Y.Z), а не сборка разработчика.
func IsRelease(v string) bool { _, ok := parse(v); return ok }

// Newer — выпуск latest новее запущенной версии current. Сборка разработчика (dev) и
// любая непонятная версия не обновляются никогда.
func Newer(current, latest string) bool {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// NewPath и OldPath — где лежат скачанная и прежняя программа: рядом с exe.
func NewPath(exe string) string { return exe + ".new" }
func OldPath(exe string) string { return exe + ".old" }

// Download скачивает выпуск в NewPath(exe) и сверяет сумму. Несовпадение — файл удаляется:
// недокачанная программа на месте рабочей хуже, чем отказ.
func Download(ctx context.Context, c *http.Client, r Release, exe, ua string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("скачивание %s: %s", r.Version, resp.Status)
	}
	dst := NewPath(exe)
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("рядом с программой нельзя записать файл: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != r.Size {
		err = fmt.Errorf("скачалось %d байт из %d", n, r.Size)
	}
	if err == nil {
		if got := hex.EncodeToString(h.Sum(nil)); got != r.SHA256 {
			err = fmt.Errorf("сумма скачанного файла не сходится с выпуском: %s вместо %s", got, r.SHA256)
		}
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// Swap ставит скачанную программу на место работающей: exe → .old, .new → exe. Не вышло
// второе — первое откатывается, и на месте остаётся прежняя программа.
func Swap(exe string) error {
	if _, err := os.Stat(NewPath(exe)); err != nil {
		return fmt.Errorf("скачанной программы нет: %w", err)
	}
	old := OldPath(exe)
	// .old от прошлого обновления, если его не успели убрать
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("не убирается %s: %w", old, err)
	}
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("программа не отодвигается: %w", err)
	}
	if err := os.Rename(NewPath(exe), exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("новая программа не встала (%v), и прежняя не вернулась: %v — она лежит в %s", err, rerr, old)
		}
		return fmt.Errorf("новая программа не встала на место: %w", err)
	}
	return nil
}

// Cleanup убирает прежнюю программу после обновления. Прежний процесс к этому времени
// может ещё не выйти и держать файл — тогда несколько попыток, потом молча: уберётся при
// следующем запуске. Заодно — недокачанный .new от прерванного обновления.
func Cleanup(exe string, tries int, pause time.Duration) error {
	os.Remove(NewPath(exe))
	var err error
	for i := 0; i < tries; i++ {
		if err = os.Remove(OldPath(exe)); err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		time.Sleep(pause)
	}
	return err
}
