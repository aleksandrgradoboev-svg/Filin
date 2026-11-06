package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Доступ. Компьютер, на котором стоит сервер, ходит без привязки — это его хозяин.
// Из сети — только привязанный телефон с токеном: заголовок Authorization: Bearer
// (запросы приложения) или кука filin_device (экраны Филина внутри приложения).
// Привязка — по одноразовому коду, который показывается на компьютере.

const deviceCookie = "filin_device"

// pairTTL и pairAttempts — сколько живёт код привязки и сколько неверных попыток он терпит.
const (
	pairTTL      = 10 * time.Minute
	pairAttempts = 5
)

type pairing struct {
	mu       sync.Mutex
	code     string
	expires  time.Time
	attempts int
	now      func() time.Time // подменяется в тестах
}

// issue выдаёт новый код из шести цифр; прежний перестаёт действовать.
func (p *pairing) issue() (string, time.Time, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", time.Time{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code = fmt.Sprintf("%06d", n.Int64())
	p.expires = p.clock().Add(pairTTL)
	p.attempts = 0
	return p.code, p.expires, nil
}

// use проверяет код. Верный код одноразовый; после pairAttempts неверных попыток
// код сгорает, чтобы шесть цифр нельзя было перебрать.
func (p *pairing) use(code string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.code == "" || p.clock().After(p.expires) {
		p.code = ""
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(p.code)) == 1 {
		p.code = ""
		return true
	}
	p.attempts++
	if p.attempts >= pairAttempts {
		p.code = ""
	}
	return false
}

func (p *pairing) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// isLocal — запрос с этого же компьютера.
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestToken(r *http.Request) string {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	if c, err := r.Cookie(deviceCookie); err == nil {
		return c.Value
	}
	return ""
}

// open — что доступно без привязки: оформление, привязка и само приложение для установки.
func open(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/static/") || p == "/manifest.webmanifest" ||
		p == "/api/pair" || p == "/api/app" || p == "/app.apk"
}

// access пускает компьютер и привязанные устройства; управление устройствами — только с компьютера.
func (s *server) access(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local := isLocal(r)
		if strings.HasPrefix(r.URL.Path, "/devices") && !local {
			http.Error(w, "телефоны привязываются и отзываются только с компьютера, на котором стоит Филин",
				http.StatusForbidden)
			return
		}
		// архив всей библиотеки по домашней сети не раздаётся — даже привязанному телефону
		if r.URL.Path == "/backup" && !local {
			http.Error(w, "резервная копия снимается только на компьютере, на котором стоит Филин", http.StatusForbidden)
			return
		}
		// и не по ссылке с чужого сайта: <img src="…/backup"> гонял бы снимок всей базы.
		// Браузер говорит, откуда запрос; нет заголовка — не браузер (curl), пускаем
		if r.URL.Path == "/backup" {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "резервная копия — только кнопкой в самом Филине", http.StatusForbidden)
				return
			}
		}
		if local || open(r) {
			h.ServeHTTP(w, r)
			return
		}
		if _, err := s.st.DeviceByToken(requestToken(r)); err != nil {
			if !errors.Is(err, catalog.ErrNotFound) {
				s.fail(w, err)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "телефон не привязан"})
				return
			}
			http.Error(w, "Филин открывается только с привязанного телефона. Привязка — в приложении Филин.",
				http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// --- привязка ---

// apiPair: телефон присылает код с экрана компьютера и своё имя, получает токен.
func (s *server) apiPair(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code, Name string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ждали JSON с полями code и name"})
		return
	}
	if !s.pair.use(req.Code) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "код неверный или устарел — откройте на компьютере «Телефоны» и получите новый"})
		return
	}
	token, err := s.st.AddDevice(req.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

type devicesPage struct {
	Title   string
	Devices []catalog.Device
	Code    string
	Expires string
	Phone   []string     // адреса Филина для телефона в домашней сети
	AppQR   template.URL // QR: скачать приложение (адрес /app.apk)
	PairQR  template.URL // QR: адрес Филина и код привязки — снимает приложение
}

// devicesPage — страница «Телефоны»; QR — только когда известен адрес в домашней сети:
// без него телефону нечего снимать.
func (s *server) devicesPage(ds []catalog.Device, code, expires string) devicesPage {
	p := devicesPage{Title: "Телефоны", Devices: ds, Code: code, Expires: expires, Phone: s.phone}
	if len(s.phone) > 0 {
		p.AppQR = qrImage(s.phone[0] + "/app.apk")
		if code != "" {
			p.PairQR = qrImage(pairText(s.phone[0], code))
		}
	}
	return p
}

func (s *server) devices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.st.Devices()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "devices", s.devicesPage(ds, "", ""))
}

func (s *server) deviceCode(w http.ResponseWriter, r *http.Request) {
	code, exp, err := s.pair.issue()
	if err != nil {
		s.fail(w, err)
		return
	}
	ds, err := s.st.Devices()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "devices", s.devicesPage(ds, code, exp.Format("15:04")))
}

func (s *server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st.RevokeDevice(id); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/devices", http.StatusSeeOther)
}
