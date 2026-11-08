// Package ocrpack — пакет распознавания Филина: ONNX Runtime и модели PaddleOCR отдельным
// файлом выпуска (filin-ocr-<N>.zip, сборка — ocr-pack/pack.ps1). Филин скачивает его сам
// при первом запуске — в фоне, с докачкой (update.Fetch), — проверяет сумму архива и каждого
// файла по описи, распаковывает рядом с библиотекой и открывает движок (internal/paddle).
// Программа при обновлении пакет не тянет заново: он меняется, только когда меняются модели.
package ocrpack

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/paddle"
	"github.com/aleksandrgradoboev-svg/filin/internal/update"
)

// Пакет, с которым собрана эта версия Филина. Сумма и размер — от ocr-pack/pack.ps1: другой
// архив по этому адресу Филин не примет.
const (
	Version = 1
	URL     = "https://github.com/aleksandrgradoboev-svg/Filin/releases/download/ocr-1/filin-ocr-1.zip"
	Size    = 28657619
	SHA256  = "bd4c96ce0ea6b8da2d062002c741bf39a639d72e02107742c8c59f1272369dc3"
)

// Pack — пакет в папке данных: состояние загрузки и открытый движок.
type Pack struct {
	dir  string // куда распаковывается: <данные>/ocr-engine-1
	zip  string // куда скачивается архив
	url  string
	size int64
	sum  string

	mu      sync.Mutex
	eng     *paddle.Engine
	state   string // что сказать человеку, пока движка нет
	done    int64
	started bool

	// для повтора после неудачи: не вышло (сеть лежала при запуске) — следующий вопрос о
	// готовности не чаще retryEvery запускает подготовку снова, без перезапуска Филина
	ctx     context.Context
	client  *http.Client
	ua      string
	lastTry time.Time
}

// retryEvery — как часто повторять неудавшуюся подготовку пакета.
var retryEvery = time.Minute

// New — пакет для папки данных Филина (там, где filin.db).
func New(dataDir string) *Pack {
	return &Pack{
		dir: filepath.Join(dataDir, fmt.Sprintf("ocr-engine-%d", Version)),
		zip: filepath.Join(dataDir, fmt.Sprintf("filin-ocr-%d.zip", Version)),
		url: URL, size: Size, sum: SHA256,
		state: "распознавание ещё не запускалось",
	}
}

// OpenDir — распакованный пакет из папки, без скачивания: для замеров и отладки.
func OpenDir(dir string) (*Pack, error) {
	if err := verifyDir(dir); err != nil {
		return nil, err
	}
	eng, err := open(dir)
	if err != nil {
		return nil, err
	}
	return &Pack{dir: dir, eng: eng}, nil
}

// Start готовит пакет в фоне: есть на диске и цел — открывает; нет — скачивает. Повторный
// вызов после неудачи пробует снова (скачанное продолжится, а не начнётся сначала).
func (p *Pack) Start(ctx context.Context, c *http.Client, ua string) {
	p.mu.Lock()
	if p.started || p.eng != nil {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.ctx, p.client, p.ua, p.lastTry = ctx, c, ua, time.Now()
	p.mu.Unlock()
	go func() {
		err := p.prepare(ctx, c, ua)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.started = false
		if err != nil {
			p.state = "распознавание не готово: " + err.Error()
			log.Printf("пакет распознавания: %v", err)
		}
	}()
}

func (p *Pack) prepare(ctx context.Context, c *http.Client, ua string) error {
	if err := verifyDir(p.dir); err != nil {
		p.setState("скачиваю модели распознавания (%d МБ) — один раз", p.size>>20)
		log.Printf("пакет распознавания %d: %v — скачиваю %s", Version, err, p.url)
		err := update.Fetch(ctx, c, p.url, p.zip, p.size, p.sum, ua, func(done, total int64) {
			p.mu.Lock()
			p.done = done
			p.mu.Unlock()
		})
		if err != nil {
			return fmt.Errorf("скачивание: %w", err)
		}
		if err := unpack(p.zip, p.dir); err != nil {
			return err
		}
		os.Remove(p.zip)
		if err := verifyDir(p.dir); err != nil {
			return err
		}
		log.Printf("пакет распознавания %d распакован: %s", Version, p.dir)
	}
	eng, err := open(p.dir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.eng = eng
	p.mu.Unlock()
	return nil
}

func (p *Pack) setState(format string, a ...any) {
	p.mu.Lock()
	p.state = fmt.Sprintf(format, a...)
	p.mu.Unlock()
}

// Status — готово ли распознавание; если нет — что сказать человеку. Прошлая подготовка не
// удалась и с неё прошло retryEvery — запускает её снова (скачанное продолжится).
func (p *Pack) Status() (bool, string) {
	p.mu.Lock()
	if p.eng != nil {
		p.mu.Unlock()
		return true, ""
	}
	if p.started && p.done > 0 {
		defer p.mu.Unlock()
		return false, fmt.Sprintf("скачиваю модели распознавания: %d из %d МБ — один раз, потом не нужно", p.done>>20, p.size>>20)
	}
	retry := !p.started && p.client != nil && time.Since(p.lastTry) >= retryEvery
	ctx, c, ua, msg := p.ctx, p.client, p.ua, p.state
	p.mu.Unlock()
	if retry {
		p.Start(ctx, c, ua)
		msg += " — пробую снова, спросите через минуту"
	}
	return false, msg
}

// Recognize — таблица слов фото в формате tesseract tsv (её разбирает ocr.ParseTSV).
func (p *Pack) Recognize(ctx context.Context, photo []byte) ([]byte, error) {
	p.mu.Lock()
	eng := p.eng
	p.mu.Unlock()
	if eng == nil {
		_, msg := p.Status()
		return nil, errors.New(msg)
	}
	img, err := cover.Decode(photo)
	if err != nil {
		return nil, err
	}
	type res struct {
		tsv []byte
		err error
	}
	ch := make(chan res, 1)
	go func() { t, err := eng.Recognize(img); ch <- res{t, err} }()
	select {
	case r := <-ch:
		return r.tsv, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("распознавание не уложилось по времени: %w", ctx.Err())
	}
}

// manifest — опись пакета: файл → размер и сумма.
type manifest struct {
	Version int `json:"version"`
	Files   map[string]struct {
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// needed — без этих файлов движок не открыть.
var needed = []string{"onnxruntime.dll", "det.onnx", "rec.onnx", "rec_dict.txt", "ori.onnx"}

// verifyDir — распакованный пакет цел: опись есть, версия наша, каждый файл — по описи.
func verifyDir(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return errors.New("пакета нет")
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("опись пакета не читается: %w", err)
	}
	if m.Version != Version {
		return fmt.Errorf("пакет версии %d, нужна %d", m.Version, Version)
	}
	for _, name := range needed {
		if _, ok := m.Files[name]; !ok {
			return fmt.Errorf("в описи пакета нет %s", name)
		}
	}
	for name, f := range m.Files {
		got, err := fileSum(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if got != f.SHA256 {
			return fmt.Errorf("%s повреждён", name)
		}
	}
	return nil
}

// unpack распаковывает архив во временную папку и ставит её на место dir целиком: Филин,
// закрытый посреди распаковки, не оставит полупакета, который выглядит готовым.
func unpack(zipPath, dir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("архив пакета не открывается: %w", err)
	}
	defer r.Close()
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	for _, f := range r.File {
		name := filepath.FromSlash(f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		// только плоские имена и licenses/…: никаких «..» и абсолютных путей из архива
		if strings.Contains(f.Name, "..") || filepath.IsAbs(name) || strings.ContainsAny(f.Name, `:\`) {
			return fmt.Errorf("в архиве пакета странное имя: %q", f.Name)
		}
		dst := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := extract(f, dst); err != nil {
			return err
		}
	}
	os.RemoveAll(dir)
	return os.Rename(tmp, dir)
}

func extract(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 512<<20)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// open — движок из распакованного пакета.
func open(dir string) (*paddle.Engine, error) {
	read := func(n string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, n)) }
	var m paddle.Models
	var err error
	if m.Det, err = read("det.onnx"); err != nil {
		return nil, err
	}
	if m.Rec, err = read("rec.onnx"); err != nil {
		return nil, err
	}
	if m.Dict, err = read("rec_dict.txt"); err != nil {
		return nil, err
	}
	if m.Ori, err = read("ori.onnx"); err != nil {
		return nil, err
	}
	start := time.Now()
	eng, err := paddle.Open(filepath.Join(dir, "onnxruntime.dll"), m, 0)
	if err != nil {
		return nil, err
	}
	log.Printf("распознавание готово (модели загружены за %.1f с)", time.Since(start).Seconds())
	return eng, nil
}
