package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// Раздача приложения для телефона — из каталога приложения (<данные>/app): filin.apk и
// app.json с номером версии. Кладёт их туда сам Филин, скачивая из выпуска, когда там
// приложение новее (update.SyncApp), — или android/publish.ps1 при разработке. В filin.exe
// приложение не вшивается: оно меняется реже программы, а весило две трети каждого
// обновления. Приложение при запуске спрашивает /api/app и, если версия новее
// установленной, скачивает /app.apk. Оба адреса открыты без привязки: приложение ставят
// раньше, чем привязывают.

const (
	apkFile     = "filin.apk"
	appInfoFile = "app.json"
	// после установки Филин сам скачивает приложение из выпуска — первые минуты его ещё нет
	appMissing = "приложения для телефона на компьютере пока нет: Филин скачивает его сам после запуска — подождите несколько минут и попробуйте снова"
)

type appInfo struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	APK         string `json:"apk"`
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

// readAppInfo — какое приложение раздаём: из каталога приложения; нет — ошибка.
func (s *server) readAppInfo() (appInfo, error) {
	if s.appDir == "" {
		return appInfo{}, os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.appDir, appInfoFile))
	if err != nil {
		return appInfo{}, err
	}
	a, err := parseAppInfo(b)
	if err != nil {
		return a, err
	}
	_, err = os.Stat(filepath.Join(s.appDir, apkFile))
	return a, err
}

func (s *server) apiApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.readAppInfo()
	if err != nil {
		apiError(w, http.StatusNotFound, appMissing)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *server) appAPK(w http.ResponseWriter, r *http.Request) {
	if _, err := s.readAppInfo(); err != nil {
		http.Error(w, appMissing, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="filin.apk"`)
	w.Header().Set("Cache-Control", "no-cache") // новая версия — тот же адрес
	http.ServeFile(w, r, filepath.Join(s.appDir, apkFile))
}
