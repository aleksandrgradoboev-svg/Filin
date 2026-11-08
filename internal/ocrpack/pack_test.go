package ocrpack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hexSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// fakeZip — пакет с поддельными моделями и верной описью; extra — лишние записи архива.
func fakeZip(t *testing.T, extra map[string][]byte) []byte {
	t.Helper()
	files := map[string][]byte{}
	for _, n := range needed {
		files[n] = []byte("подделка " + n)
	}
	files["licenses/Apache-2.0.txt"] = []byte("лицензия")
	m := map[string]any{"version": Version, "files": map[string]any{}}
	for n, b := range files {
		m["files"].(map[string]any)[n] = map[string]any{"size": len(b), "sha256": hexSum(b)}
	}
	mb, _ := json.Marshal(m)
	files["manifest.json"] = mb
	for n, b := range extra {
		files[n] = b
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnpackAndVerify(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	os.WriteFile(zp, fakeZip(t, nil), 0o644)
	dst := filepath.Join(dir, "ocr-engine-1")
	if err := unpack(zp, dst); err != nil {
		t.Fatal(err)
	}
	if err := verifyDir(dst); err != nil {
		t.Fatalf("целый пакет не принят: %v", err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Error("временная папка распаковки осталась")
	}
	// подменённая модель — пакет не годится
	os.WriteFile(filepath.Join(dst, "rec.onnx"), []byte("чужое"), 0o644)
	if err := verifyDir(dst); err == nil || !strings.Contains(err.Error(), "rec.onnx") {
		t.Errorf("подмена не замечена: %v", err)
	}
	// нет пакета вовсе
	if err := verifyDir(filepath.Join(dir, "нет")); err == nil {
		t.Error("пустая папка принята за пакет")
	}
}

// Имя в архиве не выводит за папку пакета.
func TestUnpackRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	os.WriteFile(zp, fakeZip(t, map[string][]byte{"../вне.txt": []byte("x")}), 0o644)
	if err := unpack(zp, filepath.Join(dir, "pack")); err == nil {
		t.Fatal("архив с «..» распакован")
	}
	if _, err := os.Stat(filepath.Join(dir, "вне.txt")); !os.IsNotExist(err) {
		t.Error("файл записан вне папки пакета")
	}
}

// Пакета нет — Start скачивает его, проверяет и распаковывает; архив после распаковки убран.
// Поддельная dll не откроется — Status говорит об этом человеку, а не молчит.
func TestStartDownloadsPack(t *testing.T) {
	body := fakeZip(t, nil)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write(body)
	}))
	defer srv.Close()
	data := t.TempDir()
	p := New(data)
	p.url, p.size, p.sum = srv.URL, int64(len(body)), hexSum(body)
	if ok, msg := p.Status(); ok || msg == "" {
		t.Errorf("до запуска: %v %q", ok, msg)
	}
	p.Start(context.Background(), srv.Client(), "filin/test")
	deadline := time.Now().Add(10 * time.Second)
	for {
		p.mu.Lock()
		running := p.started
		p.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("подготовка пакета не кончилась")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Errorf("скачиваний: %d", calls.Load())
	}
	if err := verifyDir(p.dir); err != nil {
		t.Errorf("пакет не распакован: %v", err)
	}
	if _, err := os.Stat(p.zip); !os.IsNotExist(err) {
		t.Error("архив остался после распаковки")
	}
	ok, msg := p.Status()
	if ok || !strings.Contains(msg, "не готово") {
		t.Errorf("поддельная dll: %v %q", ok, msg)
	}
	if _, err := p.Recognize(context.Background(), []byte("jpeg")); err == nil {
		t.Error("распознавание без движка не отказало")
	}
}

// Сеть лежала при запуске — следующий вопрос о готовности (не раньше чем через retryEvery)
// пробует скачать снова, без перезапуска Филина.
func TestStatusRetriesAfterFailure(t *testing.T) {
	old := retryEvery
	retryEvery = 0
	defer func() { retryEvery = old }()
	body := fakeZip(t, nil)
	var down atomic.Bool
	down.Store(true)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if down.Load() {
			http.NotFound(w, r) // выпуска «нет» — отказ без повторов внутри загрузки
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	p := New(t.TempDir())
	p.url, p.size, p.sum = srv.URL, int64(len(body)), hexSum(body)
	wait := func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			p.mu.Lock()
			running := p.started
			p.mu.Unlock()
			if !running {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("подготовка не кончилась")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	p.Start(context.Background(), srv.Client(), "filin/test")
	wait()
	if verifyDir(p.dir) == nil {
		t.Fatal("пакет есть, хотя сервер отказал")
	}
	down.Store(false)
	if _, msg := p.Status(); !strings.Contains(msg, "пробую снова") {
		t.Errorf("повтор не запущен: %q", msg)
	}
	wait()
	if err := verifyDir(p.dir); err != nil {
		t.Errorf("после повтора пакета нет: %v (запросов %d)", err, calls.Load())
	}
}

// Сумма архива не та — пакет не распаковывается.
func TestStartRejectsWrongSum(t *testing.T) {
	body := fakeZip(t, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	p := New(t.TempDir())
	p.url, p.size, p.sum = srv.URL, int64(len(body)), strings.Repeat("0", 64)
	if err := p.prepare(context.Background(), srv.Client(), "filin/test"); err == nil {
		t.Fatal("архив с чужой суммой принят")
	}
	if _, err := os.Stat(p.dir); !os.IsNotExist(err) {
		t.Error("архив с чужой суммой распакован")
	}
}
