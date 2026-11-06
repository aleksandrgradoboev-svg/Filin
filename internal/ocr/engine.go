package ocr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os/exec"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
)

// ErrNoEngine — Tesseract не найден: распознавание не установлено на этом компьютере.
var ErrNoEngine = errors.New("распознавание не установлено: нет программы Tesseract на компьютере с Филином")

// side — длинная сторона картинки для распознавания. Фото с телефона — 4000 пикселей и
// больше: Tesseract на них медленнее, а точнее не становится; кегль титула крупный.
const side = 2400

// Engine — вызов программы tesseract. Bin — путь к ней или имя в PATH.
type Engine struct {
	Bin string
}

// Available — найдена ли программа.
func (e Engine) Available() bool {
	_, err := exec.LookPath(e.bin())
	return err == nil
}

func (e Engine) bin() string {
	if e.Bin == "" {
		return "tesseract"
	}
	return e.Bin
}

// Recognize распознаёт фото титула и возвращает таблицу слов (вывод tsv) — её
// разбирает ParseTSV, а сохраняется она как есть, чтобы разбор можно было переиграть.
func (e Engine) Recognize(ctx context.Context, photo []byte) ([]byte, error) {
	if !e.Available() {
		return nil, ErrNoEngine
	}
	img, err := cover.Decode(photo)
	if err != nil {
		return nil, err
	}
	var in bytes.Buffer
	if err := png.Encode(&in, gray(cover.Fit(img, side))); err != nil {
		return nil, err
	}
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.bin(), "stdin", "stdout", "-l", "rus", "--psm", "3", "tsv")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("распознавание не уложилось по времени: %w", ctx.Err())
		}
		return nil, fmt.Errorf("tesseract: %v: %s", err, firstLine(stderr.String()))
	}
	return out.Bytes(), nil
}

// gray — оттенки серого: цвет бумаги и краски распознаванию только мешает.
func gray(img image.Image) *image.Gray {
	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return "без пояснений"
}
