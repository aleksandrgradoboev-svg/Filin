package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastRetries(t *testing.T) {
	t.Helper()
	st, rd := stallTimeout, retryDelay
	stallTimeout, retryDelay = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { stallTimeout, retryDelay = st, rd })
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// rangeServer отдаёт body с поддержкой Range; serve решает, сколько байт отдать в этой попытке
// и не зависнуть ли после них (имитация обрыва и простоя).
func rangeServer(t *testing.T, body []byte, serve func(n int, from int) (limit int, hang bool)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		from := 0
		if rg := r.Header.Get("Range"); rg != "" {
			v, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			from = v
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-from))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		limit, hang := serve(n, from)
		end := min(len(body), from+limit)
		w.Write(body[from:end])
		w.(http.Flusher).Flush()
		if hang {
			select { // молчим, пока клиент не оборвёт
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		if end < len(body) { // обрыв: закрываем соединение посреди файла
			hj, ok := w.(http.Hijacker)
			if ok {
				if c, _, err := hj.Hijack(); err == nil {
					c.Close()
				}
			}
		}
	}))
	t.Cleanup(s.Close)
	return s, &calls
}

// Обрыв посреди файла — следующая попытка продолжает с того же байта, а не сначала.
func TestFetchResumesAfterDrop(t *testing.T) {
	fastRetries(t)
	body := bytes.Repeat([]byte("Филин "), 20000)
	var froms []int
	s, calls := rangeServer(t, body, func(n, from int) (int, bool) {
		froms = append(froms, from)
		return 30000, false // каждая попытка — 30 КБ и обрыв
	})
	dst := filepath.Join(t.TempDir(), "filin.exe.new")
	var last int64
	err := Fetch(context.Background(), s.Client(), s.URL, dst, int64(len(body)), sum(body), "filin/test",
		func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); !bytes.Equal(b, body) {
		t.Fatal("файл собран неверно")
	}
	if froms[0] != 0 || froms[1] != 30000 || calls.Load() < 4 {
		t.Errorf("попытки начинались с %v — докачки нет", froms)
	}
	if last != int64(len(body)) {
		t.Errorf("ход загрузки кончился на %d из %d", last, len(body))
	}
	if _, err := os.Stat(PartPath(dst)); !os.IsNotExist(err) {
		t.Error(".part остался после успешной загрузки")
	}
}

// Сервер замолчал посреди файла — обрыв по простою и докачка, а не вечное ожидание.
func TestFetchStall(t *testing.T) {
	fastRetries(t)
	body := bytes.Repeat([]byte("x"), 100000)
	s, _ := rangeServer(t, body, func(n, from int) (int, bool) {
		if n == 1 {
			return 40000, true
		}
		return len(body), false
	})
	dst := filepath.Join(t.TempDir(), "f")
	start := time.Now()
	if err := Fetch(context.Background(), s.Client(), s.URL, dst, int64(len(body)), sum(body), "filin/test", nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("простой не оборван: %v", time.Since(start))
	}
}

// Сервер без Range отдаёт файл целиком — недокачанное переписывается, а не дописывается.
func TestFetchServerWithoutRange(t *testing.T) {
	fastRetries(t)
	body := bytes.Repeat([]byte("abc"), 10000)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Write(body[:5000])
			if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
				c.Close()
			}
			return
		}
		w.Write(body) // Range не понимает — всегда 200 и весь файл
	}))
	defer s.Close()
	dst := filepath.Join(t.TempDir(), "f")
	if err := Fetch(context.Background(), s.Client(), s.URL, dst, int64(len(body)), sum(body), "filin/test", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); !bytes.Equal(b, body) {
		t.Fatal("файл собран неверно")
	}
}

// Остаток от прошлого раза подхватывается: Филин закрыли посреди загрузки — при следующем
// запуске качается только хвост.
func TestFetchContinuesOldPart(t *testing.T) {
	fastRetries(t)
	body := bytes.Repeat([]byte("q"), 50000)
	dst := filepath.Join(t.TempDir(), "f")
	os.WriteFile(PartPath(dst), body[:20000], 0o644)
	var first int = -1
	s, _ := rangeServer(t, body, func(n, from int) (int, bool) {
		if first < 0 {
			first = from
		}
		return len(body), false
	})
	if err := Fetch(context.Background(), s.Client(), s.URL, dst, int64(len(body)), sum(body), "filin/test", nil); err != nil {
		t.Fatal(err)
	}
	if first != 20000 {
		t.Errorf("первая попытка с %d, ждали 20000", first)
	}
}

// Сервер, который не отдаёт ни байта, — сдаёмся после maxAttempts, а не крутимся вечно.
func TestFetchGivesUp(t *testing.T) {
	fastRetries(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "занят", http.StatusServiceUnavailable)
	}))
	defer s.Close()
	err := Fetch(context.Background(), s.Client(), s.URL, filepath.Join(t.TempDir(), "f"), 100, strings.Repeat("0", 64), "filin/test", nil)
	if err == nil || !strings.Contains(err.Error(), "подряд") {
		t.Fatalf("ошибка %v", err)
	}
}

// Сервер без докачки, который каждый раз обрывается на одном месте, — не вечный цикл:
// продвижения дальше прежнего нет, и после maxAttempts загрузка сдаётся.
func TestFetchNoRangeAlwaysDrops(t *testing.T) {
	fastRetries(t)
	body := bytes.Repeat([]byte("z"), 50000)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body[:10000])
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			c.Close()
		}
	}))
	defer s.Close()
	done := make(chan error, 1)
	go func() {
		done <- Fetch(context.Background(), s.Client(), s.URL, filepath.Join(t.TempDir(), "f"), int64(len(body)), sum(body), "filin/test", nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("недокачанный файл принят")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("загрузка крутится вечно")
	}
}

// 404 — повторять бессмысленно.
func TestFetchNotFound(t *testing.T) {
	fastRetries(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer s.Close()
	if err := Fetch(context.Background(), s.Client(), s.URL, filepath.Join(t.TempDir(), "f"), 100, strings.Repeat("0", 64), "filin/test", nil); err == nil {
		t.Fatal("404 принят")
	}
	if calls.Load() != 1 {
		t.Errorf("на 404 повторов: %d", calls.Load())
	}
}
