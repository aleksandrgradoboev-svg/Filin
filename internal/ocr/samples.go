package ocr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Samples — каталог проб распознавания: на каждое фото титула папка с самим фото,
// выводом Tesseract, черновиком и тем, что человек в итоге сохранил. По ним видно,
// сколько полей угадано, и разбор можно переиграть на тех же фото после правки правил.
type Samples string

var sampleRe = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{6}$`)

// ErrBadSample — номер пробы не нашего вида (из запроса пришло что-то чужое).
var ErrBadSample = errors.New("неизвестная проба распознавания")

// Save кладёт пробу и возвращает её номер.
func (s Samples) Save(photo, tsv []byte, d Draft) (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
	dir := filepath.Join(string(s), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), photo, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "ocr.tsv"), tsv, 0o644); err != nil {
		return "", err
	}
	return id, writeJSON(filepath.Join(dir, "draft.json"), d)
}

// Load читает черновик пробы.
func (s Samples) Load(id string) (Draft, error) {
	var d Draft
	dir, err := s.dir(id)
	if err != nil {
		return d, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		return d, ErrBadSample
	}
	return d, json.Unmarshal(data, &d)
}

// Finish записывает к пробе итог: что сохранено в карточке и под каким номером книги.
func (s Samples) Finish(id string, final any) error {
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "final.json"), final)
}

func (s Samples) dir(id string) (string, error) {
	if !sampleRe.MatchString(id) {
		return "", ErrBadSample
	}
	dir := filepath.Join(string(s), id)
	if _, err := os.Stat(dir); err != nil {
		return "", ErrBadSample
	}
	return dir, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
