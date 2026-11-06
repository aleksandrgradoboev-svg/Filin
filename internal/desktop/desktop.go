// Package desktop — Филин как программа на компьютере: адреса в домашней сети, один
// экземпляр, браузер, значок в трее, ярлыки. Сервер живёт, пока программа запущена:
// закрыли Филина — телефон его не видит (решено 06.11.2026, вечный сервер не нужен).
package desktop

import (
	"context"
	_ "embed"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed filin.ico
var Icon []byte

// HomeIP — адрес этого компьютера в домашней сети: тот, с которого компьютер выходит
// наружу (через роутер). Перебор всех частных адресов не годится: адаптеры VPN (OpenVPN TAP,
// Hyper-V) тоже частные и даже с аппаратным адресом — проверено 06.11.2026: рядом с адресом
// роутера 192.168.x.x нашёлся 10.x.x.x от OpenVPN. nil — домашней сети нет.
func HomeIP() net.IP {
	// UDP «соединение» ничего не отправляет: система лишь выбирает, с какого адреса пошла
	// бы связь наружу, — это и есть сетевая карта с маршрутом по умолчанию
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return nil
	}
	defer c.Close()
	a, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil
	}
	if ip := a.IP.To4(); ip != nil && ip.IsPrivate() {
		return ip
	}
	return nil // публичный адрес или VPN на весь трафик — в такую сеть Филина не выставляем
}

// FindTesseract — где программа распознавания титулов, если её не указали: пусто — есть в
// PATH (её найдут по имени) или нет вовсе. Установщик Tesseract для Windows в PATH её не
// добавляет, поэтому смотрим и в его обычные папки.
func FindTesseract() string {
	if _, err := exec.LookPath("tesseract"); err == nil {
		return ""
	}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")} {
		if base == "" || base == "Programs" {
			continue
		}
		p := filepath.Join(base, "Tesseract-OCR", "tesseract.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Running — отвечает ли на этом порту компьютера уже запущенный Филин: /api/ping
// возвращает want (web.PingBody).
func Running(port, want string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/ping", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return resp.StatusCode == http.StatusOK && strings.TrimSpace(string(b)) == want
}
