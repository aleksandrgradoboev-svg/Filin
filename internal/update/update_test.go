package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		want        bool
	}{
		{"v0.6.0", "v0.6.1", true},
		{"v0.6.0", "v0.7.0", true},
		{"v0.6.9", "v0.10.0", true}, // числа, а не строки: «10» больше «9»
		{"v0.9.0", "v1.0.0", true},
		{"v0.6.0", "v0.6.0", false},
		{"v0.6.1", "v0.6.0", false}, // откат не предлагаем
		{"dev", "v9.9.9", false},    // сборка разработчика не обновляется
		{"v0.6.0", "v0.7.0-rc1", false},
		{"v0.6.0", "0.7.0", false},
		{"v0.6.0", "v0.7", false},
		{"v0.6.0", "v0.07.0", false},
		{"v0.6.0", "", false},
	} {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

// release — ответ API GitHub в той форме, какую он отдаёт (сверено с выпуском v0.6.0).
func release(tag string, assets ...string) string {
	return fmt.Sprintf(`{"tag_name":%q,"html_url":"https://example/rel","assets":[%s]}`, tag, strings.Join(assets, ","))
}

func asset(name, url, digest string, size int) string {
	return fmt.Sprintf(`{"name":%q,"browser_download_url":%q,"digest":%q,"size":%d}`, name, url, digest, size)
}

func serveJSON(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "filin/test" {
			http.Error(w, "нет подписи программы", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestLatest(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	s := serveJSON(t, release("v0.6.1",
		asset("filin.apk", "https://x/apk", "sha256:"+strings.Repeat("00", 32), 10),
		asset("filin.exe", "https://x/exe", "sha256:"+strings.ToUpper(sum), 1234)))
	r, err := Latest(context.Background(), s.Client(), s.URL, "filin/test")
	if err != nil {
		t.Fatal(err)
	}
	want := Release{Version: "v0.6.1", URL: "https://x/exe", SHA256: sum, Size: 1234, Page: "https://example/rel"}
	if r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}
}

func TestLatestRefuses(t *testing.T) {
	sum := "sha256:" + strings.Repeat("ab", 32)
	for name, body := range map[string]string{
		"нет exe":         release("v0.6.1", asset("filin.apk", "u", sum, 1)),
		"нет суммы":       release("v0.6.1", asset("filin.exe", "u", "", 1)),
		"не та сумма":     release("v0.6.1", asset("filin.exe", "u", "md5:abc", 1)),
		"короткая сумма":  release("v0.6.1", asset("filin.exe", "u", "sha256:abc", 1)),
		"нулевой размер":  release("v0.6.1", asset("filin.exe", "u", sum, 0)),
		"огромный размер": release("v0.6.1", asset("filin.exe", "u", sum, maxSize+1)),
		"плохой тег":      release("latest", asset("filin.exe", "u", sum, 1)),
		"не JSON":         "<html>",
	} {
		t.Run(name, func(t *testing.T) {
			s := serveJSON(t, body)
			if _, err := Latest(context.Background(), s.Client(), s.URL, "filin/test"); err == nil {
				t.Fatal("выпуск принят, а должен быть отказ")
			}
		})
	}
	t.Run("ошибка сервера", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "rate limit", http.StatusForbidden)
		}))
		defer s.Close()
		if _, err := Latest(context.Background(), s.Client(), s.URL, "filin/test"); err == nil {
			t.Fatal("ответ 403 принят")
		}
	})
}

func exeServer(t *testing.T, body []byte) (*httptest.Server, Release) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(s.Close)
	h := sha256.Sum256(body)
	return s, Release{Version: "v0.6.1", URL: s.URL + "/filin.exe", SHA256: hex.EncodeToString(h[:]), Size: int64(len(body))}
}

func TestDownloadAndSwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "filin.exe")
	os.WriteFile(exe, []byte("старая"), 0o755)
	s, r := exeServer(t, []byte("новая программа"))
	if err := Download(context.Background(), s.Client(), r, exe, "filin/test"); err != nil {
		t.Fatal(err)
	}
	if err := Swap(exe); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{exe: "новая программа", OldPath(exe): "старая"} {
		if b, _ := os.ReadFile(p); string(b) != want {
			t.Errorf("%s: %q, want %q", filepath.Base(p), b, want)
		}
	}
	if _, err := os.Stat(NewPath(exe)); !os.IsNotExist(err) {
		t.Error(".new остался после перестановки")
	}
	if err := Cleanup(exe, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(OldPath(exe)); !os.IsNotExist(err) {
		t.Error(".old не убран")
	}
}

func TestDownloadRejectsBadFile(t *testing.T) {
	for name, tweak := range map[string]func(*Release){
		"чужая сумма":   func(r *Release) { r.SHA256 = strings.Repeat("00", 32) },
		"не тот размер": func(r *Release) { r.Size++ },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "filin.exe")
			os.WriteFile(exe, []byte("старая"), 0o755)
			s, r := exeServer(t, []byte("новая программа"))
			tweak(&r)
			if err := Download(context.Background(), s.Client(), r, exe, "filin/test"); err == nil {
				t.Fatal("битый файл принят")
			}
			if _, err := os.Stat(NewPath(exe)); !os.IsNotExist(err) {
				t.Error("битый .new не удалён")
			}
			if b, _ := os.ReadFile(exe); string(b) != "старая" {
				t.Error("рабочая программа тронута")
			}
		})
	}
}

func TestSwapWithoutDownload(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "filin.exe")
	os.WriteFile(exe, []byte("старая"), 0o755)
	if err := Swap(exe); err == nil {
		t.Fatal("перестановка без скачанного файла прошла")
	}
	if b, _ := os.ReadFile(exe); string(b) != "старая" {
		t.Error("рабочая программа тронута")
	}
}

// TestSwapRunningExe — главное допущение пакета: работающую программу Windows не даёт
// перезаписать, но даёт переименовать. Проверяем на самом тестовом бинаре: он сейчас
// работает. Копию не берём — нужен именно запущенный файл.
func TestSwapRunningExe(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(NewPath(self), []byte("заглушка"), 0o755); err != nil {
		t.Skip("папка тестового бинаря не для записи:", err)
	}
	orig, _ := os.ReadFile(self)
	defer func() { // вернуть как было: тестовый бинарь go удалит сам, но пусть уйдёт целым
		os.Remove(self)
		os.Rename(OldPath(self), self)
		os.Remove(NewPath(self))
	}()
	if err := Swap(self); err != nil {
		t.Fatalf("работающая программа не переставилась: %v", err)
	}
	if b, _ := os.ReadFile(self); string(b) != "заглушка" {
		t.Error("на месте программы не новый файл")
	}
	if b, _ := os.ReadFile(OldPath(self)); len(b) != len(orig) {
		t.Error("прежняя программа не в .old")
	}
}
