package web

import (
	"net/url"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// crumb — уровень темы в карточке: название и ссылка на все книги с этим уровнем.
type crumb struct {
	Name string
	URL  string
}

// crumbLine — тема для показа. Длинная цепочка (у РГБ бывает по девять уровней) не должна
// съедать экран телефона: видны начало и два последних уровня — самые конкретные,
// середина свёрнута и раскрывается по «…».
type crumbLine struct {
	Head, Middle, Tail []crumb
}

const crumbsShown = 4 // столько уровней показывается без сворачивания

func subjectURL(level string) string { return "/?" + url.Values{"t": {level}}.Encode() }

func crumbs(path string) crumbLine {
	var all []crumb
	for _, l := range catalog.SubjectLevels(path) {
		all = append(all, crumb{l, subjectURL(l)})
	}
	if len(all) <= crumbsShown {
		return crumbLine{Head: all}
	}
	return crumbLine{Head: all[:1], Middle: all[1 : len(all)-2], Tail: all[len(all)-2:]}
}
