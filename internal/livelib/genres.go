package livelib

import (
	"errors"
	"regexp"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// ErrNoGenres — в файле нет справочника жанров.
var ErrNoGenres = errors.New("в файле не найден справочник жанров — нужна страница livelib.ru/genres, сохранённая целиком")

// Страница livelib.ru/genres: после заголовка «Все жанры» — карточки разделов
// (main-genre-title) со списком жанров (subgenre). До заголовка — «ваши жанры» со
// счётчиками по своим книгам: это не справочник, его пропускаем.
var (
	reSection  = regexp.MustCompile(`class="main-genre-title"[^>]*>([^<]+)</a>`)
	reSubgenre = regexp.MustCompile(`<div class="subgenre"[^>]*>\s*<a href="[^"]*/genre/[^"]*">([^<]+)</a>`)
)

// ParseGenresMHTML разбирает сохранённую страницу справочника жанров.
func ParseGenresMHTML(data []byte) ([]catalog.GenreSection, error) {
	page, _, err := splitMHTML(data)
	if err != nil {
		return nil, err
	}
	_, all, ok := strings.Cut(page, "<h1>Все жанры</h1>")
	if !ok {
		return nil, ErrNoGenres
	}
	var out []catalog.GenreSection
	for _, block := range strings.Split(all, `class="card-white genre-block"`)[1:] {
		m := reSection.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		sec := catalog.GenreSection{Name: clean(m[1])}
		for _, sub := range reSubgenre.FindAllStringSubmatch(block, -1) {
			if name := clean(sub[1]); name != "" {
				sec.Children = append(sec.Children, name)
			}
		}
		out = append(out, sec)
	}
	if len(out) == 0 {
		return nil, ErrNoGenres
	}
	return out, nil
}
