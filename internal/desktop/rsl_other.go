//go:build !windows

package desktop

import "errors"

// Вне Windows встроенного браузера нет: поиск — в своём браузере, ссылка на запись — в форму.

var ErrRSLBusy = errors.New("окно поиска в РГБ уже открыто")

func PickRSL(pageURL string) (string, bool, error) {
	return "", false, errors.New("поиск в окне есть только в Windows — найдите запись в браузере и вставьте ссылку")
}
