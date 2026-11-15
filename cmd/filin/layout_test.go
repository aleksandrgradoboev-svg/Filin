package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// oldFolder — папка Филина, как её раскладывали до подпапок (E:\Филин на 15.11.2026).
func oldFolder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"filin.exe":                         "exe",
		"filin.ico":                         "ico",
		".ярлыки-спрошены":                  "да",
		"filin.db":                          "база",
		"filin.db-wal":                      "wal",
		"filin.log":                         "журнал",
		"filin.log.1":                       "старый журнал",
		"filin-перед-v1.2.0-2026-11-15.zip": "копия",
		"filin-ocr-1.zip.0123456789ab.part": "недокачано",
		`covers\a.jpg`:                      "обложка",
		`ocr\5\final.json`:                  "{}",
		`ocr-engine-1\manifest.json`:        "{}",
		`app\filin.apk`:                     "apk",
		`app\app.json`:                      "{}",
		`копия-перед-v1.0.3-ручная\filin.db`:           "ручная",
		`до-восстановления-2026-11-01-120000\filin.db`: "отложено",
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Errorf("нет %s: %v", p, err)
	}
	return string(b)
}

func TestArrange(t *testing.T) {
	root := oldFolder(t)
	l := layoutIn(root)
	if !l.old || l.DB != filepath.Join(root, "filin.db") {
		t.Fatalf("старая папка не опознана: %+v", l)
	}
	n, err := arrange(root, time.Date(2026, 11, 15, 20, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 { // база, wal, обложки, пробы, отложенное, копия, модели, недокачанное, два журнала, телефон
		t.Errorf("перемещено %d, ждал 11", n)
	}
	l = layoutIn(root)
	if l.old || l.DB != filepath.Join(root, "библиотека", "filin.db") {
		t.Fatalf("после раскладки: %+v", l)
	}
	for p, want := range map[string]string{
		l.DB:                             "база",
		l.DB + "-wal":                    "wal",
		filepath.Join(l.Covers, "a.jpg"): "обложка",
		filepath.Join(root, "библиотека", "ocr", "5", "final.json"):                          "{}",
		filepath.Join(root, "библиотека", "до-восстановления-2026-11-01-120000", "filin.db"): "отложено",
		filepath.Join(l.Copies, "filin-перед-v1.2.0-2026-11-15.zip"):                         "копия",
		filepath.Join(l.Copies, "перед-раскладкой-2026-11-15-200000", "filin.db"):            "база",
		filepath.Join(l.Copies, "перед-раскладкой-2026-11-15-200000", "filin.db-wal"):        "wal",
		filepath.Join(l.Models, "ocr-engine-1", "manifest.json"):                             "{}",
		filepath.Join(l.Models, "filin-ocr-1.zip.0123456789ab.part"):                         "недокачано",
		filepath.Join(l.Phone, "filin.apk"):                                                  "apk",
		filepath.Join(l.Log, "filin.log"):                                                    "журнал",
		filepath.Join(l.Log, "filin.log.1"):                                                  "старый журнал",
		// программа и то, что не наше, — на месте
		filepath.Join(root, "filin.exe"):                             "exe",
		filepath.Join(root, "filin.ico"):                             "ico",
		filepath.Join(root, ".ярлыки-спрошены"):                      "да",
		filepath.Join(root, "копия-перед-v1.0.3-ручная", "filin.db"): "ручная",
	} {
		if got := read(t, p); got != want {
			t.Errorf("%s: %q, ждал %q", p, got, want)
		}
	}
	for _, gone := range []string{"filin.db", "covers", "ocr", "app", "filin.log", "ocr-engine-1"} {
		if exists(filepath.Join(root, gone)) {
			t.Errorf("%s остался в корне", gone)
		}
	}
}

func TestArrangeRefusesOverwrite(t *testing.T) {
	root := oldFolder(t)
	if err := os.MkdirAll(filepath.Join(root, "модели", "ocr-engine-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := arrange(root, time.Now()); err == nil || !strings.Contains(err.Error(), "ничего не трогал") {
		t.Fatalf("ждал отказ, получил %v", err)
	}
	if read(t, filepath.Join(root, "filin.db")) != "база" || !exists(filepath.Join(root, "covers", "a.jpg")) {
		t.Error("после отказа что-то сдвинулось")
	}
}

// Сбой посреди перемещений: всё уже перемещённое возвращается, Филин остаётся по-старому.
func TestArrangeRollsBack(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("занятый файл не даёт себя переместить только в Windows")
	}
	root := oldFolder(t)
	defer func(n int) { renameRetries = n }(renameRetries)
	renameRetries = 2
	busy, err := os.Open(filepath.Join(root, "filin.log")) // журнал держит открытым «другой процесс»
	if err != nil {
		t.Fatal(err)
	}
	_, err = arrange(root, time.Now())
	busy.Close()
	if err == nil || !strings.Contains(err.Error(), "возвращено") {
		t.Fatalf("ждал откат, получил %v", err)
	}
	l := layoutIn(root)
	if !l.old {
		t.Fatalf("после отката раскладка новая: %+v", l)
	}
	for _, p := range []string{"filin.db", `covers\a.jpg`, `app\filin.apk`, `ocr-engine-1\manifest.json`, "filin-перед-v1.2.0-2026-11-15.zip"} {
		if !exists(filepath.Join(root, p)) {
			t.Errorf("%s не вернулся", p)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(root, "копии", "перед-раскладкой-*")); len(left) > 0 {
		t.Errorf("после чистого отката осталась копия: %v", left)
	}
}

// Прежний Филин после обновления ещё держит журнал — раскладка дожидается, а не откатывается.
func TestArrangeWaitsForBusyFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("занятый файл не даёт себя переместить только в Windows")
	}
	root := oldFolder(t)
	busy, err := os.Open(filepath.Join(root, "filin.log"))
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(time.Second, func() { busy.Close() }) // прежний процесс вышел через секунду
	if _, err := arrange(root, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(root, "журнал", "filin.log")) {
		t.Error("журнал не переехал")
	}
}

func TestLayoutIn(t *testing.T) {
	root := t.TempDir()
	if l := layoutIn(root); l.old || l.DB != filepath.Join(root, "библиотека", "filin.db") {
		t.Errorf("пустая папка — новая раскладка: %+v", l)
	}
	legacy := filepath.Join(root, "data", "filin.db")
	os.MkdirAll(filepath.Dir(legacy), 0o755)
	os.WriteFile(legacy, nil, 0o644)
	if l := layoutIn(root); l.old || l.DB != legacy {
		t.Errorf("data\\filin.db не узнан: %+v", l)
	}
}
