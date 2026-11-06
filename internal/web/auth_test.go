package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

func TestPairingCode(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	p := pairing{now: func() time.Time { return now }}
	code, _, err := p.issue()
	if err != nil || len(code) != 6 {
		t.Fatalf("код: %q %v", code, err)
	}
	if !p.use(code) || p.use(code) {
		t.Error("код должен сработать ровно один раз")
	}

	code, _, _ = p.issue()
	for range pairAttempts {
		p.use("000000x")
	}
	if p.use(code) {
		t.Error("после пяти неверных попыток код должен сгореть")
	}

	code, _, _ = p.issue()
	now = now.Add(pairTTL + time.Second)
	if p.use(code) {
		t.Error("просроченный код сработал")
	}
}

// netApp — обработчик Филина и запросы к нему «из сети» и «с компьютера».
type netApp struct {
	t    *testing.T
	h    http.Handler
	st   *catalog.Store
	data string // каталог данных: база, covers/, app/
}

func newNetApp(t *testing.T, opts ...Option) *netApp {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "filin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := catalog.New(d)
	h, err := New(st, filepath.Join(dir, "covers"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return &netApp{t, h, st, dir}
}

// do шлёт запрос; from — адрес отправителя («192.168.1.50:5000» — телефон в сети, «127.0.0.1:5000» — компьютер).
func (a *netApp) do(from, method, path, token, ctype string, body []byte) (int, []byte) {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, bytes.NewReader(body))
	r.RemoteAddr = from
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, b
}

const (
	phone = "192.168.1.50:5000"
	local = "127.0.0.1:5000"
)

func TestNetworkNeedsPairedPhone(t *testing.T) {
	a := newNetApp(t)
	if code, _ := a.do(phone, "GET", "/", "", "", nil); code != http.StatusUnauthorized {
		t.Errorf("каталог из сети без привязки: %d", code)
	}
	if code, b := a.do(phone, "GET", "/api/me", "", "", nil); code != http.StatusUnauthorized || !bytes.Contains(b, []byte("error")) {
		t.Errorf("API из сети без привязки: %d %s", code, b)
	}
	if code, _ := a.do(phone, "GET", "/api/me", "чужой-токен", "", nil); code != http.StatusUnauthorized {
		t.Errorf("чужой токен пропущен: %d", code)
	}
	if code, _ := a.do(phone, "GET", "/static/app.css", "", "", nil); code != http.StatusOK {
		t.Errorf("оформление закрыто: %d", code)
	}
	if code, _ := a.do(local, "GET", "/", "", "", nil); code != http.StatusOK {
		t.Errorf("с компьютера каталог закрыт: %d", code)
	}
	if code, _ := a.do(phone, "POST", "/devices/code", "", "", nil); code != http.StatusForbidden {
		t.Errorf("код привязки выдан в сеть: %d", code)
	}
}

// Весь путь телефона: код на компьютере → привязка → скан → книга с полкой и обложкой → отзыв.
func TestPhoneFlow(t *testing.T) {
	a := newNetApp(t)
	_, page := a.do(local, "POST", "/devices/code", "", "", nil)
	m := regexp.MustCompile(`class="code">(\d{6})<`).FindSubmatch(page)
	if m == nil {
		t.Fatal("на странице «Телефоны» нет кода")
	}
	if code, _ := a.do(phone, "POST", "/api/pair", "", "application/json", []byte(`{"code":"999999x","name":"Realme"}`)); code != http.StatusForbidden {
		t.Errorf("неверный код принят: %d", code)
	}
	code, b := a.do(phone, "POST", "/api/pair", "", "application/json", []byte(`{"code":"`+string(m[1])+`","name":"Realme"}`))
	var pair struct{ Token string }
	if code != http.StatusOK || json.Unmarshal(b, &pair) != nil || len(pair.Token) != 64 {
		t.Fatalf("привязка: %d %s", code, b)
	}
	tok := pair.Token

	// скан: книги ещё нет
	code, b = a.do(phone, "GET", "/api/isbn/978-0-306-40615-7", tok, "", nil)
	if code != http.StatusOK || !bytes.Contains(b, []byte(`"books":[]`)) {
		t.Fatalf("скан неизвестной книги: %d %s", code, b)
	}
	// заводим с полкой
	code, b = a.do(phone, "POST", "/api/books", tok, "application/json", []byte(`{"title":"Белый Клык","authors":["Лондон Джек"],
		"isbn":"0306406152","year":1984,"with_copy":true,"room":"Кабинет","bookcase":"Шкаф 1","shelf":"2"}`))
	var added struct {
		ID     int64 `json:"id"`
		CopyID int64 `json:"copy_id"`
	}
	if code != http.StatusCreated || json.Unmarshal(b, &added) != nil || added.CopyID == 0 {
		t.Fatalf("добавление: %d %s", code, b)
	}
	// фото обложки
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 300, 450)))
	if code, b = a.do(phone, "PUT", "/api/books/1/cover", tok, "image/png", img.Bytes()); code != http.StatusOK {
		t.Fatalf("обложка: %d %s", code, b)
	}
	// повторный скан: книга есть, с автором, обложкой и экземпляром
	_, b = a.do(phone, "GET", "/api/isbn/9780306406157", tok, "", nil)
	var found struct {
		Books []apiBook `json:"books"`
	}
	json.Unmarshal(b, &found)
	if len(found.Books) != 1 || found.Books[0].Authors[0] != "Лондон Джек" || found.Books[0].Cover == "" || found.Books[0].Copies != 1 {
		t.Fatalf("повторный скан: %s", b)
	}
	if code, _ := a.do(phone, "POST", "/api/books/1/copies", tok, "application/json", []byte(`{}`)); code != http.StatusCreated {
		t.Errorf("ещё экземпляр: %d", code)
	}
	if code, b := a.do(phone, "GET", "/api/shelves", tok, "", nil); code != http.StatusOK || !strings.Contains(string(b), "Кабинет") {
		t.Errorf("полки: %d %s", code, b)
	}
	// экраны Филина в приложении — по куке
	r := httptest.NewRequest("GET", "http://192.168.1.10:8080/book/1", nil)
	r.RemoteAddr = phone
	r.AddCookie(&http.Cookie{Name: deviceCookie, Value: tok})
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("карточка по куке: %d", w.Code)
	}

	// отзыв с компьютера — токен перестаёт работать сразу
	ds, _ := a.st.Devices()
	if len(ds) != 1 || ds[0].Name != "Realme" || ds[0].LastSeen == "" {
		t.Fatalf("устройства: %+v", ds)
	}
	a.do(local, "POST", "/devices/1/revoke", "", "", nil)
	if code, _ := a.do(phone, "GET", "/api/me", tok, "", nil); code != http.StatusUnauthorized {
		t.Errorf("отозванный телефон пропущен: %d", code)
	}
}

// Приложение отдаётся без привязки (его ставят до привязки); пока не выложено — 404.
func TestAppDownload(t *testing.T) {
	a := newNetApp(t)
	if code, _ := a.do(phone, "GET", "/api/app", "", "", nil); code != http.StatusNotFound {
		t.Errorf("пустой каталог приложения: %d", code)
	}
	app := filepath.Join(a.data, "app")
	os.MkdirAll(app, 0o755)
	os.WriteFile(filepath.Join(app, "app.json"), []byte(`{"version_code": 3, "version_name": "0.3.0"}`), 0o644)
	if code, _ := a.do(phone, "GET", "/api/app", "", "", nil); code != http.StatusNotFound {
		t.Errorf("сведения без самого APK отдаются: %d", code)
	}
	os.WriteFile(filepath.Join(app, "filin.apk"), []byte("PK-не-настоящий"), 0o644)
	code, b := a.do(phone, "GET", "/api/app", "", "", nil)
	var info appInfo
	if code != http.StatusOK || json.Unmarshal(b, &info) != nil || info.VersionCode != 3 || info.APK != "/app.apk" {
		t.Fatalf("сведения о приложении: %d %s", code, b)
	}
	r := httptest.NewRequest("GET", "http://192.168.1.10:8080/app.apk", nil)
	r.RemoteAddr = phone
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/vnd.android.package-archive" ||
		w.Body.String() != "PK-не-настоящий" {
		t.Errorf("скачивание APK: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}

// Приложение, вшитое в выпуск: человек скачал один filin.exe — QR «поставить приложение»
// ведёт к APK сразу. Выложенное в папку новее — отдаётся оно, вшитое новее — вшитое.
func TestBundledApp(t *testing.T) {
	get := func(a *netApp) (int, string, string) {
		_, b := a.do(phone, "GET", "/api/app", "", "", nil)
		var info appInfo
		json.Unmarshal(b, &info)
		r := httptest.NewRequest("GET", "http://192.168.1.10:8080/app.apk", nil)
		r.RemoteAddr = phone
		w := httptest.NewRecorder()
		a.h.ServeHTTP(w, r)
		return info.VersionCode, w.Body.String(), w.Header().Get("Content-Type")
	}
	bundled := BundledApp([]byte(`{"version_code": 14, "version_name": "0.6.0"}`), []byte("PK-вшитый"))

	a := newNetApp(t, bundled) // папки app нет вовсе
	if v, body, ct := get(a); v != 14 || body != "PK-вшитый" || ct != "application/vnd.android.package-archive" {
		t.Errorf("только вшитое: %d %q %q", v, body, ct)
	}

	publish := func(a *netApp, code int, body string) {
		app := filepath.Join(a.data, "app")
		os.MkdirAll(app, 0o755)
		os.WriteFile(filepath.Join(app, "app.json"), []byte(fmt.Sprintf(`{"version_code": %d}`, code)), 0o644)
		os.WriteFile(filepath.Join(app, "filin.apk"), []byte(body), 0o644)
	}
	publish(a, 15, "PK-выложенный")
	if v, body, _ := get(a); v != 15 || body != "PK-выложенный" {
		t.Errorf("выложенное новее: %d %q", v, body)
	}
	publish(a, 13, "PK-старый")
	if v, body, _ := get(a); v != 14 || body != "PK-вшитый" {
		t.Errorf("вшитое новее выложенного: %d %q", v, body)
	}
}

// Год вне 1400–2100 с телефона не принимается — как и в веб-форме.
func TestAPIRejectsBadYear(t *testing.T) {
	a := newNetApp(t)
	if code, b := a.do(local, "POST", "/api/books", "", "application/json", []byte(`{"title":"x","year":19855}`)); code != http.StatusBadRequest {
		t.Errorf("год 19855 принят: %d %s", code, b)
	}
}

// Страница «Телефоны»: QR установки приложения сразу, QR привязки — с кодом, и в нём адрес
// Филина и тот же код, что показан цифрами.
func TestDevicesQR(t *testing.T) {
	a := newNetApp(t, PhoneURLs([]string{"http://192.168.1.10:8080"}))
	_, b := a.do(local, "GET", "/devices", "", "", nil)
	page := string(b)
	if strings.Count(page, `src="data:image/png;base64,`) != 1 || !strings.Contains(page, "Поставить приложение") {
		t.Errorf("до кода — один QR (приложение):\n%s", page)
	}
	_, b = a.do(local, "POST", "/devices/code", "", "", nil)
	page = string(b)
	if strings.Count(page, `src="data:image/png;base64,`) != 2 {
		t.Errorf("с кодом — два QR")
	}
	if got := pairText("http://192.168.1.10:8080", "123456"); got != "filin:pair?code=123456&server=http%3A%2F%2F192.168.1.10%3A8080" {
		t.Errorf("текст QR привязки: %s", got)
	}

	// без домашней сети QR нет — снимать нечего, а предупреждение есть
	a = newNetApp(t)
	_, b = a.do(local, "GET", "/devices", "", "", nil)
	if strings.Contains(string(b), "data:image/png") || !strings.Contains(string(b), "Домашняя сеть не найдена") {
		t.Error("без адреса в сети: QR или нет предупреждения")
	}
}
