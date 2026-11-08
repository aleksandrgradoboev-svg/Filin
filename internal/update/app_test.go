package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appServer — выпуск с приложением отдельными файлами: app.json с номером версии и filin.apk.
func appServer(t *testing.T, code int, apk []byte) (*httptest.Server, Release) {
	t.Helper()
	info := []byte(fmt.Sprintf(`{"version_code": %d, "version_name": "0.8.%d"}`, code, code))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app.json":
			w.Write(info)
		case "/filin.apk":
			w.Write(apk)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s, Release{Version: "v1.1.2",
		APK:     File{URL: s.URL + "/filin.apk", SHA256: sum(apk), Size: int64(len(apk))},
		AppInfo: File{URL: s.URL + "/app.json"}}
}

func writeApp(t *testing.T, dir string, code int, apk string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, AppInfo), []byte(fmt.Sprintf(`{"version_code": %d}`, code)), 0o644)
	os.WriteFile(filepath.Join(dir, AppAPK), []byte(apk), 0o644)
}

// В выпуске приложение новее — кладётся в каталог: APK целиком, потом сведения.
func TestSyncAppNewer(t *testing.T) {
	apk := []byte("PK-новое приложение 0.8.2")
	s, rel := appServer(t, 18, apk)
	dir := filepath.Join(t.TempDir(), "app")
	writeApp(t, dir, 17, "PK-старое")
	got, err := SyncApp(context.Background(), s.Client(), rel, dir, "filin/test")
	if err != nil || got != 18 {
		t.Fatalf("SyncApp: %d %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, AppAPK)); string(b) != string(apk) {
		t.Errorf("APK: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, AppInfo)); !strings.Contains(string(b), `"version_code": 18`) {
		t.Errorf("app.json: %s", b)
	}
}

// Та же версия — ничего не качается; выпуск без приложения отдельными файлами — тоже.
func TestSyncAppNothingToDo(t *testing.T) {
	s, rel := appServer(t, 17, []byte("PK"))
	dir := filepath.Join(t.TempDir(), "app")
	writeApp(t, dir, 17, "PK-тот же")
	if got, err := SyncApp(context.Background(), s.Client(), rel, dir, "filin/test"); err != nil || got != 17 {
		t.Errorf("та же версия: %d %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, AppAPK)); string(b) != "PK-тот же" {
		t.Error("та же версия скачана заново")
	}
	if got, err := SyncApp(context.Background(), s.Client(), Release{Version: "v1.1.1"}, dir, "filin/test"); err != nil || got != 0 {
		t.Errorf("выпуск без приложения: %d %v", got, err)
	}
}

// Сведения есть, а файла нет (удалили руками, первый запуск на новом компьютере) — качается.
func TestSyncAppMissingAPK(t *testing.T) {
	apk := []byte("PK-0.8.1")
	s, rel := appServer(t, 17, apk)
	dir := filepath.Join(t.TempDir(), "app")
	writeApp(t, dir, 17, "")
	os.Remove(filepath.Join(dir, AppAPK))
	if got, err := SyncApp(context.Background(), s.Client(), rel, dir, "filin/test"); err != nil || got != 17 {
		t.Fatalf("SyncApp: %d %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, AppAPK)); string(b) != string(apk) {
		t.Error("APK не скачан")
	}
}

// Сумма APK не сходится — сведения не меняются: телефон не получит битое приложение.
func TestSyncAppBadSum(t *testing.T) {
	s, rel := appServer(t, 18, []byte("PK-новое"))
	rel.APK.SHA256 = strings.Repeat("0", 64)
	dir := filepath.Join(t.TempDir(), "app")
	writeApp(t, dir, 17, "PK-старое")
	if _, err := SyncApp(context.Background(), s.Client(), rel, dir, "filin/test"); err == nil {
		t.Fatal("битый APK принят")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, AppInfo)); !strings.Contains(string(b), "17") {
		t.Errorf("сведения поменялись при битом APK: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, AppAPK)); string(b) != "PK-старое" {
		t.Error("рабочий APK испорчен")
	}
}

// Latest находит в выпуске приложение отдельными файлами.
func TestLatestFindsApp(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.1.2","html_url":"x","assets":[
			{"name":"filin.exe","browser_download_url":"https://e/x.exe","digest":"sha256:%s","size":100},
			{"name":"filin.apk","browser_download_url":"https://e/x.apk","digest":"sha256:%s","size":50},
			{"name":"app.json","browser_download_url":"https://e/app.json","size":40}]}`, sha, sha)
	}))
	defer s.Close()
	r, err := Latest(context.Background(), s.Client(), s.URL, "filin/test")
	if err != nil {
		t.Fatal(err)
	}
	if r.APK.URL != "https://e/x.apk" || r.APK.SHA256 != sha || r.APK.Size != 50 || r.AppInfo.URL != "https://e/app.json" {
		t.Errorf("приложение в выпуске: %+v %+v", r.APK, r.AppInfo)
	}
}
