//go:build !windows

package desktop

import "errors"

// Вне Windows встроенного браузера нет: поиск — в своём браузере, ссылка на запись — в форму.

var ErrRSLBusy = errors.New("поиск в РГБ уже идёт — дождитесь его")

type RSLFound struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Info   string `json:"info"`
}

type RSLResults struct {
	Query string     `json:"query"`
	Info  string     `json:"info"`
	Items []RSLFound `json:"items"`
}

func SearchRSL(pageURL string, next []string) (RSLResults, error) {
	return RSLResults{}, errors.New("поиск РГБ из Филина есть только в Windows — найдите запись в браузере и вставьте ссылку")
}
