package web

import (
	"net/http"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

type seriesPage struct {
	Title string
	Items []catalog.SeriesItem
}

// series — что из серии есть, по номерам.
func (s *server) series(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	title, items, err := s.st.Series(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "series", seriesPage{title, items})
}

type genresPage struct {
	Title      string
	Tree       []catalog.GenreTreeNode
	HasLiveLib bool
}

// genres — справочник жанров по разделам со счётчиками своих книг.
func (s *server) genres(w http.ResponseWriter, r *http.Request) {
	tree, err := s.st.GenreTree()
	if err != nil {
		s.fail(w, err)
		return
	}
	p := genresPage{Title: "Жанры", Tree: tree}
	for _, n := range tree {
		if n.Name != "" {
			p.HasLiveLib = true
		}
	}
	s.render(w, "genres", p)
}
