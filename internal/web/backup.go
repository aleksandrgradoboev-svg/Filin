package web

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/backup"
)

// PingBody — ответ /api/ping: по нему второй запуск программы узнаёт уже работающего Филина.
const PingBody = "filin"

// PhoneURLs — адреса Филина в домашней сети, которые человек вводит в приложении телефона.
func PhoneURLs(urls []string) Option { return func(s *server) { s.phone = urls } }

// backup отдаёт архив всей библиотеки: база и обложки. Куда его положить, решает человек.
// Только с компьютера — проверка в access.
func (s *server) backup(w http.ResponseWriter, r *http.Request) {
	cw := &countingWriter{ResponseWriter: w, name: backup.FileName(time.Now())}
	if err := backup.Write(r.Context(), cw, s.st.DB(), string(s.covers), os.TempDir()); err != nil {
		log.Printf("резервная копия: %v", err)
		if cw.n == 0 { // до архива дело не дошло — можно честно ответить ошибкой
			http.Error(w, "резервная копия не снялась: "+err.Error(), http.StatusInternalServerError)
		} // иначе архив оборвётся, и браузер покажет недокачанный файл
	}
}

// countingWriter ставит заголовки архива при первой записи: пока ни байта не ушло,
// ошибку снимка базы ещё можно отдать обычной страницей.
type countingWriter struct {
	http.ResponseWriter
	name string
	n    int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.n == 0 {
		h := c.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", `attachment; filename="`+c.name+`"`)
		h.Set("Cache-Control", "no-store")
	}
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}
