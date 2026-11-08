package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Удаление книги — отдельной страницей, а не confirm() в карточке: на ней видно, что уйдёт
// вместе с книгой (оценки, история выдач), и она не зависит от того, показывает ли
// WebView приложения диалоги JavaScript.

func (s *server) deleteBookForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := s.st.Edition(id)
	if errors.Is(err, catalog.ErrNotFound) {
		// книга уже удалена: «назад» в приложении после удаления приводит сюда — в каталог
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	f, err := s.st.EditionFootprint(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "book_delete", struct {
		Title string
		E     catalog.Edition
		F     catalog.Footprint
	}{"Удалить книгу: " + e.Title, e, f})
}

func (s *server) deleteBook(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	gone, err := s.st.DeleteEdition(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.covers.Remove(gone.Cover); err != nil { // книга удалена; лишний файл — не повод для ошибки
		log.Printf("книга %d удалена, обложка %s осталась: %v", id, gone.Cover, err)
	}
	title := gone.Title
	if gone.Volume != "" {
		title += " " + gone.Volume
	}
	http.Redirect(w, r, "/?"+url.Values{"deleted": {title}}.Encode(), http.StatusSeeOther)
}
