package web

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/export"
)

// Program — версия Филина: пишется в выгрузку JSON, чтобы было видно, кто её снял.
func Program(v string) Option { return func(s *server) { s.program = v } }

// exportPage — страница «Выгрузка»: каталог таблицей и целиком.
func (s *server) exportPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "export", struct{ Title string }{"Выгрузка"})
}

// exportFile отдаёт каталог файлом. Собирается в памяти целиком, а не потоком: ошибка
// чтения базы на середине дала бы обрезанную таблицу, похожую на целую. Только с
// компьютера — проверка в access, как у резервной копии.
func (s *server) exportFile(w http.ResponseWriter, r *http.Request) {
	// шаблон ServeMux берёт только сегмент целиком — формат разбирается здесь
	var ext, ctype string
	var write func(io.Writer, *export.Library) error
	switch r.PathValue("file") {
	case "catalog.csv":
		ext, ctype, write = "csv", "text/csv; charset=utf-8", export.CSV
	case "catalog.json":
		ext, ctype, write = "json", "application/json; charset=utf-8", export.JSON
	default:
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	lib, err := export.Build(s.st, s.program, now)
	var buf bytes.Buffer
	if err == nil {
		err = write(&buf, lib)
	}
	if err != nil {
		log.Printf("выгрузка %s: %v", ext, err)
		http.Error(w, "выгрузка не собралась: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", `attachment; filename="`+export.FileName(now, ext)+`"`)
	h.Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

// wholeLibrary — адреса, отдающие библиотеку целиком: резервная копия и выгрузка.
// Их не раздают по сети и не отдают по ссылке с чужого сайта.
func wholeLibrary(path string) bool {
	return path == "/backup" || path == "/export" || strings.HasPrefix(path, "/export/")
}
