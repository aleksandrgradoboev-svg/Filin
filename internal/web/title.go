package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
	"github.com/aleksandrgradoboev-svg/filin/internal/ocr"
)

// Карточка с фото титула — для книг без штрихкода: телефон присылает фото, сервер
// распознаёт его и отдаёт черновик полей в форму. Сохраняет человек, после правки.

// Tesseract — путь к программе распознавания; пусто — искать tesseract в PATH.
func Tesseract(bin string) Option { return func(s *server) { s.ocr.Bin = bin } }

// recognizeTimeout — сколько ждать Tesseract: страница на домашней машине — секунды.
const recognizeTimeout = 90 * time.Second

func (s *server) apiTitle(w http.ResponseWriter, r *http.Request) {
	if !s.ocr.Available() {
		apiError(w, http.StatusServiceUnavailable, ocr.ErrNoEngine.Error())
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		apiError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("фото больше %d МБ", maxUpload>>20))
		return
	case err != nil:
		apiError(w, http.StatusBadRequest, "фото не дошло целиком: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), recognizeTimeout)
	defer cancel()
	tsv, err := s.ocr.Recognize(ctx, data)
	if errors.Is(err, cover.ErrNotImage) || errors.Is(err, cover.ErrTooLarge) {
		apiError(w, http.StatusBadRequest, "титул: "+err.Error())
		return
	}
	if err != nil {
		apiError(w, http.StatusBadGateway, err.Error())
		return
	}
	d := ocr.Parse(ocr.ParseTSV(tsv))
	// проба — для разбора качества; не записалась — черновик всё равно нужен человеку
	id, err := s.probes.Save(data, tsv, d)
	if err != nil {
		log.Printf("проба распознавания не сохранена: %v", err)
		id = ""
	}
	writeJSON(w, http.StatusOK, struct {
		Sample string `json:"sample"`
		ocr.Draft
	}{id, d})
}

// applyProbe дополняет карточку тем, что распознано со страницы, но в форму телефона не
// попадает: участники, шифры, страницы, аннотация. Заполняет только пустое — запись РГБ,
// если человек её выбрал, точнее распознавания. Возвращает ISBN со страницы.
func (s *server) applyProbe(e *catalog.Edition, sample string) []isbn.ISBN {
	if sample == "" {
		return nil
	}
	d, err := s.probes.Load(sample)
	if err != nil {
		log.Printf("проба %s: %v", sample, err)
		return nil
	}
	have := map[catalog.Person]bool{}
	for _, p := range e.People {
		have[p] = true
	}
	for _, p := range d.People {
		cp := catalog.Person{Name: p.Name, Role: p.Role}
		if !have[cp] {
			e.People = append(e.People, cp)
			have[cp] = true
		}
	}
	fill := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	fill(&e.BBK, d.BBK)
	fill(&e.UDK, d.UDK)
	fill(&e.AuthorSign, d.AuthorSign)
	fill(&e.OrigLanguage, d.OrigLanguage)
	fill(&e.Annotation, d.Annotation)
	if e.Pages == 0 {
		e.Pages = d.Pages
	}
	good, _ := catalog.ParseISBNs(d.ISBN...) // нечитаемый номер — не повод отказать в сохранении
	return good
}

// finishProbe дописывает к пробе, что человек сохранил: так видно, какие поля угаданы.
func (s *server) finishProbe(sample string, bookID int64, title, publisher, city string, year int, authors []string) {
	if sample == "" {
		return
	}
	err := s.probes.Finish(sample, map[string]any{"book": bookID, "title": title, "authors": authors,
		"publisher": publisher, "city": city, "year": year})
	if err != nil {
		log.Printf("проба %s: итог не записан: %v", sample, err)
	}
}
