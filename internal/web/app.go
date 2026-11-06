package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Раздача приложения для телефона. Источников два:
//   - выложенное: android/publish.ps1 кладёт в каталог приложения (<данные>/app) filin.apk
//     и app.json с номером версии;
//   - вшитое в filin.exe при сборке выпуска (пакет bundled) — чтобы человек, скачавший с
//     выпуска один filin.exe, мог сразу поставить приложение по QR.
// Отдаётся то, что новее. Приложение при запуске спрашивает /api/app и, если версия
// новее установленной, скачивает /app.apk. Оба адреса открыты без привязки: приложение
// ставят раньше, чем привязывают.

const (
	apkFile     = "filin.apk"
	appInfoFile = "app.json"
)

type appInfo struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	APK         string `json:"apk"`
}

// BundledApp передаёт вшитое приложение: app.json и APK (из пакета bundled).
func BundledApp(info, apk []byte) Option {
	return func(s *server) { s.bundledInfo, s.bundledAPK = info, apk }
}

func parseAppInfo(b []byte) (appInfo, error) {
	var a appInfo
	if err := json.Unmarshal(b, &a); err != nil {
		return a, err
	}
	if a.VersionCode <= 0 {
		return a, errors.New(appInfoFile + ": нет version_code")
	}
	a.APK = "/app.apk"
	return a, nil
}

// readAppInfo — какое приложение раздаём и откуда; bundled=true — вшитое.
func (s *server) readAppInfo() (a appInfo, bundled bool, err error) {
	err = os.ErrNotExist
	if s.appDir != "" {
		if b, e := os.ReadFile(filepath.Join(s.appDir, appInfoFile)); e == nil {
			if a, err = parseAppInfo(b); err == nil {
				_, err = os.Stat(filepath.Join(s.appDir, apkFile))
			}
		}
	}
	if len(s.bundledAPK) > 0 {
		if in, e := parseAppInfo(s.bundledInfo); e == nil && (err != nil || in.VersionCode > a.VersionCode) {
			return in, true, nil
		}
	}
	return a, false, err
}

func (s *server) apiApp(w http.ResponseWriter, r *http.Request) {
	a, _, err := s.readAppInfo()
	if err != nil {
		apiError(w, http.StatusNotFound, "приложение на сервер не выложено")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *server) appAPK(w http.ResponseWriter, r *http.Request) {
	_, bundled, err := s.readAppInfo()
	if err != nil {
		http.Error(w, "приложение на сервер не выложено", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="filin.apk"`)
	w.Header().Set("Cache-Control", "no-cache") // новая версия — тот же адрес
	if bundled {
		http.ServeContent(w, r, apkFile, time.Time{}, bytes.NewReader(s.bundledAPK))
		return
	}
	http.ServeFile(w, r, filepath.Join(s.appDir, apkFile))
}
