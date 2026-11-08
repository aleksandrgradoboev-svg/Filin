package main

import (
	"context"
	"net/http"

	"github.com/aleksandrgradoboev-svg/filin/internal/ocrpack"
)

// startOCR — распознавание фото титулов: пакет моделей рядом с библиотекой. Нет его — Филин
// скачивает в фоне (один раз, с докачкой); пока качается, распознавание отвечает, сколько
// осталось, а всё остальное работает как обычно.
func startOCR(dataDir string) *ocrpack.Pack {
	p := ocrpack.New(dataDir)
	p.Start(context.Background(), http.DefaultClient, "filin/"+version)
	return p
}
