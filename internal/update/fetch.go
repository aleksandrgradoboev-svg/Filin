package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Скачивание большого файла по медленному каналу. Общего предела по времени нет: 50 МБ
// на 50 КБ/с — это 17 минут, и любой разумный предел однажды оказывается меньше (так
// выпуск 1.0.3 не встал у 1.0.2: обрыв на 15-й минуте и каждый раз с нуля). Вместо него:
//   - обрыв по простою — если stallTimeout не пришло ни байта;
//   - докачка — скачанное копится в <dst>.part, повтор просит у сервера продолжение
//     (заголовок Range), а не начинает сначала;
//   - сумма SHA-256 — по всему файлу, в конце: не сошлась — файл удаляется.

var (
	// stallTimeout — сколько ждать хоть одного байта, прежде чем оборвать и повторить.
	stallTimeout = 2 * time.Minute
	// retryDelay — пауза перед повтором.
	retryDelay = 5 * time.Second
	// maxAttempts — сколько раз подряд повторять без единого нового байта.
	maxAttempts = 5
)

// Progress — сколько скачано из скольких; зовётся по ходу скачивания.
type Progress func(done, total int64)

// PartPath — недокачанный файл.
func PartPath(dst string) string { return dst + ".part" }

// errFatal — ошибка, после которой повторять бессмысленно (404, неверная сумма).
type errFatal struct{ error }

// Fetch скачивает url в dst: size байт с суммой sum (sha256, шестнадцатеричная). При обрыве
// продолжает с того места, где оборвалось; сдаётся после maxAttempts попыток подряд без
// прогресса или по отмене ctx — недокачанное остаётся в PartPath(dst) до следующего раза.
func Fetch(ctx context.Context, c *http.Client, url, dst string, size int64, sum, ua string, progress Progress) error {
	if size <= 0 || size > maxSize {
		return fmt.Errorf("странный размер файла: %d", size)
	}
	part := PartPath(dst)
	fails := 0
	best := fileSize(part) // самый дальний байт, до которого доходили: прогресс — только его рост
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		have := fileSize(part)
		if have > size { // чужой или испорченный остаток
			os.Remove(part)
			have = 0
		}
		if have == size {
			break
		}
		_, err := fetchOnce(ctx, c, url, part, have, size, ua, progress)
		now := fileSize(part)
		if err == nil && now == size {
			break
		}
		var fatal errFatal
		if errors.As(err, &fatal) {
			return fatal.error
		}
		if err == nil {
			err = errors.New("сервер закрыл соединение раньше конца файла")
		}
		last = err
		// прогресс — только продвижение дальше прежнего: сервер без докачки, который каждый раз
		// отдаёт начало файла и обрывается, иначе крутил бы загрузку вечно
		if now > best {
			best = now
			fails = 0
		} else if fails++; fails >= maxAttempts {
			return fmt.Errorf("скачивание прервалось %d раз подряд, скачано %d из %d байт: %w", fails, fileSize(part), size, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	if got, err := fileSHA256(part); err != nil {
		return err
	} else if got != sum {
		os.Remove(part)
		return fmt.Errorf("сумма скачанного файла не сходится с выпуском: %s вместо %s", got, sum)
	}
	os.Remove(dst)
	return os.Rename(part, dst)
}

// fetchOnce — одна попытка: дописывает в part с позиции from; возвращает, сколько байт пришло.
func fetchOnce(ctx context.Context, c *http.Client, url, part string, from, size int64, ua string, progress Progress) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, errFatal{err}
	}
	req.Header.Set("User-Agent", ua)
	if from > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-")
	}
	// простой считается и до ответа: сервер, который принял соединение и молчит, — тот же обрыв
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case resp.StatusCode == http.StatusPartialContent && from > 0:
		if start, ok := rangeStart(resp.Header.Get("Content-Range")); !ok || start != from {
			return 0, fmt.Errorf("сервер прислал не тот кусок: %q", resp.Header.Get("Content-Range"))
		}
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // продолжения сервер не умеет — заново
		from = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part) // остаток не подходит к файлу на сервере
		return 0, errors.New("сервер не узнал недокачанный кусок, начинаю заново")
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout:
		return 0, fmt.Errorf("сервер ответил %s", resp.Status)
	default:
		return 0, errFatal{fmt.Errorf("сервер ответил %s", resp.Status)}
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return 0, errFatal{fmt.Errorf("рядом с программой нельзя записать файл: %w", err)}
	}
	buf := make([]byte, 64<<10)
	var got int64
	body := io.LimitReader(resp.Body, size-from)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			stall.Reset(stallTimeout)
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return got, errFatal{werr}
			}
			got += int64(n)
			if progress != nil {
				progress(from+got, size)
			}
		}
		if rerr == io.EOF {
			if from == 0 && got < size && resp.StatusCode == http.StatusOK {
				// весь файл пришёл и он короче заявленного — повтор дал бы то же самое
				f.Close()
				os.Remove(part)
				return got, errFatal{fmt.Errorf("скачалось %d байт из %d", got, size)}
			}
			break
		}
		if rerr != nil {
			f.Close()
			if ctx.Err() != nil { // оборвал простой (или отмена сверху — её увидит Fetch)
				return got, fmt.Errorf("%v без новых данных: %w", stallTimeout, rerr)
			}
			return got, rerr
		}
	}
	return got, f.Close()
}

// rangeStart — начало куска из «bytes 100-199/1000».
func rangeStart(cr string) (int64, bool) {
	rest, ok := strings.CutPrefix(cr, "bytes ")
	if !ok {
		return 0, false
	}
	start, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(start, 10, 64)
	return n, err == nil
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
