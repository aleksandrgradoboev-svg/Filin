package livelib

import (
	"path/filepath"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

// Разметка — как на livelib.ru/genres; блок «ваши жанры» до заголовка в справочник не идёт.
const genresPage = `<html><body>
<div class="subgenre"> <a href="https://www.livelib.ru/genre/X"> Мой личный жанр </a> &nbsp;<span class="genre-books-count"> 3 </span> </div>
<h1>Все жанры</h1>
<div class="card-white genre-block"><div class="genre-title"><a class="main-genre-title" href="https://www.livelib.ru/genre/A">Классическая литература</a></div>
 <div class="subgenre" style=""> <a href="https://www.livelib.ru/genre/B">Русская классика</a>&nbsp;<span class="genre-books-count">5</span></div>
 <div class="subgenre" id="subgenre-more-1"><a id="subgenre-a-more-1">Показать все 29 жанров</a></div>
 <div class="subgenre" style=""> <a href="https://www.livelib.ru/genre/C">Зарубежная классика</a></div></div>
<div class="card-white genre-block"><div class="genre-title"><a class="main-genre-title" href="https://www.livelib.ru/genre/D">Зарубежная литература</a></div>
 <div class="subgenre" style=""> <a href="https://www.livelib.ru/genre/C">Зарубежная классика</a></div></div>
</body></html>`

func TestGenresImport(t *testing.T) {
	tree, err := ParseGenresMHTML(mhtmlPage(t, genresPage))
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree[0].Name != "Классическая литература" || len(tree[0].Children) != 2 ||
		tree[0].Children[1] != "Зарубежная классика" {
		t.Fatalf("дерево: %+v", tree)
	}
	if _, err := ParseGenresMHTML(mhtml(t)); err == nil {
		t.Error("страница полки разобралась как справочник жанров")
	}

	d, err := db.Open(filepath.Join(t.TempDir(), "filin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st := catalog.New(d)
	for range 2 { // повторный импорт ничего не задваивает
		if n, err := st.ImportGenreTree(tree); err != nil || n != 4 {
			t.Fatalf("импорт: %d %v", n, err)
		}
	}
	// жанр, который стоит в двух разделах, — один жанр в обоих
	nodes, err := st.GenreTree()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || len(nodes[0].Children)+len(nodes[1].Children) != 3 {
		t.Fatalf("разделы: %+v", nodes)
	}
	// книга с жанром и снятие жанра: справочный жанр не убирается вместе с последней книгой
	id, err := st.AddEdition(&catalog.Edition{Title: "Война и мир", Genres: []string{"русская классика", "своё"}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := st.Edition(id)
	if e.Genres[0] != "Русская классика" {
		t.Errorf("жанр из формы не сошёлся со справочным: %q", e.Genres)
	}
	e.Genres = nil
	if err := st.UpdateEdition(&e); err != nil {
		t.Fatal(err)
	}
	gs, _ := st.Genres()
	if len(gs) != 4 {
		t.Errorf("справочник после снятия жанров: %d жанров, ждали 4 (своё — убрано)", len(gs))
	}
}
