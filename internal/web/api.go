package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
)

// API для приложения на телефоне. Всё, кроме привязки, — под токеном (см. access).
// Ответы — JSON; ошибка — {"error": "текст для человека"}.

type apiBook struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	Publisher string   `json:"publisher"`
	Year      int      `json:"year,omitempty"`
	Volume    string   `json:"volume,omitempty"`
	Cover     string   `json:"cover,omitempty"` // адрес картинки на сервере
	Copies    int      `json:"copies"`
	URL       string   `json:"url"` // карточка в веб-части
}

func (s *server) toAPIBook(e catalog.Edition) (apiBook, error) {
	b := apiBook{ID: e.ID, Title: e.Title, Publisher: e.Publisher, Year: e.Year, Volume: e.Volume,
		URL: fmt.Sprintf("/book/%d", e.ID), Authors: []string{}}
	for _, p := range e.People {
		if p.Role == "author" || p.Role == "" {
			b.Authors = append(b.Authors, p.Name)
		}
	}
	if e.Cover != "" {
		b.Cover = "/cover/" + e.Cover
	}
	cs, err := s.st.Copies(e.ID)
	b.Copies = len(cs)
	return b, err
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *server) apiFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		apiError(w, http.StatusNotFound, "не найдено")
	case catalog.IsInput(err):
		apiError(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, err)
	}
}

// apiMe — проверка токена: приложение спрашивает при запуске, жив ли доступ.
func (s *server) apiMe(w http.ResponseWriter, r *http.Request) {
	name := "этот компьютер"
	if d, err := s.st.DeviceByToken(requestToken(r)); err == nil {
		name = d.Name
	}
	writeJSON(w, http.StatusOK, map[string]string{"device": name})
}

// apiISBN — что уже есть с этим номером. Пустой список — книги нет, можно заводить.
func (s *server) apiISBN(w http.ResponseWriter, r *http.Request) {
	n, err := isbn.Parse(r.PathValue("isbn"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "это не ISBN")
		return
	}
	found, err := s.st.EditionsByISBN(n.Norm)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := struct {
		ISBN  string    `json:"isbn"`
		Valid bool      `json:"valid"`
		Books []apiBook `json:"books"`
	}{ISBN: n.Norm, Valid: n.Valid, Books: []apiBook{}}
	for _, f := range found {
		e, err := s.st.Edition(f.ID)
		if err != nil {
			s.apiFail(w, err)
			return
		}
		b, err := s.toAPIBook(e)
		if err != nil {
			s.apiFail(w, err)
			return
		}
		out.Books = append(out.Books, b)
	}
	writeJSON(w, http.StatusOK, out)
}

type apiShelf struct {
	Room     string `json:"room"`
	Bookcase string `json:"bookcase"`
	Shelf    string `json:"shelf"`
}

func (s *server) copyFromShelf(editionID int64, sh apiShelf) (int64, error) {
	c := catalog.Copy{}
	err := s.saveCopy(editionID, copyFields{Room: strings.TrimSpace(sh.Room), Bookcase: strings.TrimSpace(sh.Bookcase),
		Shelf: strings.TrimSpace(sh.Shelf)}, &c)
	return c.ID, err
}

// apiAddBook — книга со сканера: короткая карточка и, если стоит дома, экземпляр на полке.
func (s *server) apiAddBook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string   `json:"title"`
		Authors   []string `json:"authors"`
		ISBN      string   `json:"isbn"`
		Publisher string   `json:"publisher"`
		Year      int      `json:"year"`
		WithCopy  bool     `json:"with_copy"`
		RSL       string   `json:"rsl"` // номер записи РГБ, выбранной человеком: из неё — вся карточка
		City      string   `json:"city"`
		OCR       string   `json:"ocr"` // проба распознавания титула, из которой заполнена форма
		apiShelf
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "ждали JSON с карточкой книги")
		return
	}
	if req.Year != 0 && (req.Year < 1400 || req.Year > 2100) { // те же границы, что у формы в веб-части
		apiError(w, http.StatusBadRequest, "год — четыре цифры, например 1987")
		return
	}
	var authors []catalog.Person
	for _, a := range req.Authors {
		for _, n := range splitNames(a) {
			authors = append(authors, catalog.Person{Name: n, Role: "author"})
		}
	}
	good, bad := catalog.ParseISBNs(req.ISBN)
	if len(bad) > 0 {
		apiError(w, http.StatusBadRequest, "не разобран ISBN: "+strings.Join(bad, ", "))
		return
	}
	var e catalog.Edition
	var lives map[string]string
	if req.RSL != "" {
		c, err := s.rslCard(r, req.RSL)
		if err != nil {
			if catalog.IsInput(err) {
				apiError(w, http.StatusBadRequest, err.Error())
			} else {
				apiError(w, http.StatusBadGateway, err.Error())
			}
			return
		}
		e, lives = c.Edition, c.Lives
	}
	good = append(good, s.applyProbe(&e, req.OCR)...)
	mergeForm(&e, req.Title, req.Publisher, req.Year, authors, good)
	if c := strings.TrimSpace(req.City); c != "" {
		e.City = c
	}
	if e.Title == "" {
		apiError(w, http.StatusBadRequest, "нужно название")
		return
	}
	id, err := s.st.AddEdition(&e)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	s.saveLives(lives)
	var names []string
	for _, p := range e.People {
		if p.Role == "author" {
			names = append(names, p.Name)
		}
	}
	s.finishProbe(req.OCR, id, e.Title, e.Publisher, e.City, e.Year, names)
	out := map[string]any{"id": id, "url": fmt.Sprintf("/book/%d", id)}
	if req.WithCopy {
		cid, err := s.copyFromShelf(id, req.apiShelf)
		if err != nil {
			s.apiFail(w, err)
			return
		}
		out["copy_id"] = cid
	}
	writeJSON(w, http.StatusCreated, out)
}

// apiAddCopy — «это она, ещё экземпляр» после скана.
func (s *server) apiAddCopy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	if _, err := s.st.Edition(id); err != nil {
		s.apiFail(w, err)
		return
	}
	var sh apiShelf
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&sh); err != nil {
			apiError(w, http.StatusBadRequest, "ждали JSON с полкой")
			return
		}
	}
	cid, err := s.copyFromShelf(id, sh)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"copy_id": cid})
}

// apiCover — фото обложки с камеры телефона: тело запроса — сама картинка.
func (s *server) apiCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	if _, err := s.st.Edition(id); err != nil {
		s.apiFail(w, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		apiError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("фото больше %d МБ", maxUpload>>20))
		return
	case err != nil: // оборвалась связь посреди загрузки — так и сказать, а не «слишком большое»
		apiError(w, http.StatusBadRequest, "фото не дошло целиком: "+err.Error())
		return
	}
	jpeg, err := cover.Process(data)
	if err != nil {
		apiError(w, http.StatusBadRequest, "обложка: "+err.Error())
		return
	}
	if err := s.covers.Replace(id, jpeg, func(name string) (string, error) { return s.st.SetCover(id, name) }); err != nil {
		s.apiFail(w, err)
		return
	}
	e, err := s.st.Edition(id)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"cover": "/cover/" + e.Cover})
}

// apiShelves — полки для подсказок в форме приложения.
func (s *server) apiShelves(w http.ResponseWriter, r *http.Request) {
	shs, err := s.st.Shelves()
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiShelf{}
	for _, sh := range shs {
		out = append(out, apiShelf{sh.Room, sh.Bookcase, sh.Shelf})
	}
	writeJSON(w, http.StatusOK, out)
}
