package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/backup"
	"github.com/aleksandrgradoboev-svg/filin/internal/desktop"
	"github.com/aleksandrgradoboev-svg/filin/internal/update"
)

// updatesURL — где спрашивать последний выпуск; пусто — не проверять. Подменяется для
// проверки обновления на подставном выпуске, ярлык его не запоминает. Не nil и без main:
// пусто — значит выключено.
var updatesURL = new(string)

// restartExe — обновление встало: после закрытия этой программы запустить новую. Запуск —
// в main, когда база уже закрыта и порт свободен: иначе новый Филин увидит старый на порту,
// решит «уже запущен» и тихо выйдет. Пишется из горутины обновления, читается в main.
var restartExe atomic.Value // string

// restartedEnv — пометка для запущенной после обновления версии: каталог в браузере уже
// открыт, второй вкладки не нужно.
const restartedEnv = "FILIN_RESTARTED"

// keepCopies — сколько копий «перед обновлением» хранить: последних двух хватает, чтобы
// вернуться на шаг назад, а каждая весит как вся библиотека с обложками.
const keepCopies = 2

// updater — самообновление программы из выпусков GitHub. Спрашивает человека, перед
// заменой делает копию библиотеки (новая версия может перестроить базу, и вернуться назад
// можно будет только из копии), потом закрывает Филина и запускает новый.
type updater struct {
	busy    atomic.Bool // идёт проверка или обновление
	appBusy atomic.Bool // докачивается приложение для телефона

	// mu охраняет closing и начало замены: Add после начала Wait — гонка, поэтому замена
	// начинается только под mu и только пока программа не закрывается
	mu       sync.Mutex
	closing  bool           // человек закрыл Филина — новую замену не начинать, новую версию не запускать
	applying sync.WaitGroup // идёт замена: программа не выходит, пока она не кончится
	// stop прерывает скачивание: закрытие Филина не ждёт загрузку (она без предела по
	// времени — на медленном канале минут 20), недокачанное продолжится при следующем запуске
	stop context.CancelFunc

	d      *sql.DB
	path   string // база
	covers string
	quit   func() // закрыть программу: сова → «Закрыть Филин»
}

// supported — можно ли обновляться: только выпуск под Windows. Сборку разработчика (dev)
// не обновляем никогда — иначе запуск из исходников подменился бы выпуском.
func supported() bool {
	return runtime.GOOS == "windows" && *updatesURL != "" && update.IsRelease(version)
}

// exePath — где лежит эта программа, без ярлыков-ссылок: переставлять надо сам файл.
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// check проверяет выпуск и предлагает обновиться. manual — человек сам нажал «Проверить
// обновления»: тогда о любом исходе говорим окном; при запуске — молчим обо всём, кроме
// найденной новой версии, и только пишем в журнал.
func (u *updater) check(manual bool) {
	if !u.busy.CompareAndSwap(false, true) {
		if manual {
			desktop.Message("Филин", "Проверка обновлений уже идёт.")
		}
		return
	}
	defer u.busy.Store(false)

	say := func(text string) {
		log.Print("обновление: ", text)
		if manual {
			desktop.Message("Филин — обновление", text)
		}
	}
	if !supported() {
		say("эта сборка Филина (" + version + ") не обновляется сама")
		return
	}
	exe, err := exePath()
	if err != nil {
		say("не найден путь к программе: " + err.Error())
		return
	}
	ua := "filin/" + version
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	rel, err := update.Latest(ctx, http.DefaultClient, *updatesURL, ua)
	cancel()
	if err != nil {
		say("не удалось узнать последнюю версию: " + err.Error())
		return
	}
	// приложение для телефона — отдельно от программы и в фоне: новое в выпуске — докачать
	// в каталог приложения, откуда его возьмёт телефон
	go u.syncApp(rel, ua)
	if !update.Newer(version, rel.Version) {
		say("стоит последняя версия, " + version + ".")
		return
	}
	dir, _ := filepath.Abs(filepath.Dir(u.path))
	if !desktop.Ask("Филин — обновление", fmt.Sprintf("Вышел Филин %s, сейчас стоит %s.\n\nОбновить сейчас? "+
		"Филин закроется и откроется снова через несколько секунд.\n\nПеред обновлением в папке\n%s\n"+
		"сохранится копия библиотеки — на случай, если понадобится вернуться.\n\nЧто нового: %s",
		rel.Version, version, dir, rel.Page)) {
		log.Printf("обновление до %s отложено", rel.Version)
		return
	}
	u.mu.Lock()
	if u.closing { // пока висел вопрос, Филина закрыли: база уже закрывается
		u.mu.Unlock()
		log.Printf("обновление до %s отменено: Филин закрыт", rel.Version)
		return
	}
	u.applying.Add(1)
	ctx, stop := context.WithCancel(context.Background())
	u.stop = stop
	u.mu.Unlock()
	err = u.apply(ctx, exe, rel, ua)
	u.mu.Lock()
	u.stop = nil
	closingNow := u.closing
	u.mu.Unlock()
	stop()
	u.applying.Done()
	if err != nil {
		log.Printf("обновление до %s: %v", rel.Version, err)
		if !closingNow { // закрыли посреди скачивания — это не сбой, окно с ошибкой ни к чему
			desktop.Message("Филин — обновление не встало", err.Error()+"\n\nФилин работает в прежней версии, "+version+".")
		}
		return
	}
	u.mu.Lock()
	closing := u.closing
	u.mu.Unlock()
	if closing { // закрыли во время замены: новая версия запустится со следующим запуском
		log.Printf("обновление до %s встало; Филин закрыт, перезапуск не нужен", rel.Version)
		return
	}
	log.Printf("обновление до %s встало, перезапуск", rel.Version)
	restartExe.Store(exe)
	u.quit()
}

// syncApp кладёт в каталог приложения (<данные>/app) приложение для телефона из выпуска,
// если там оно новее. Одновременно — одно; оборвалось — докачается при следующей проверке.
func (u *updater) syncApp(rel update.Release, ua string) {
	if !u.appBusy.CompareAndSwap(false, true) {
		return
	}
	defer u.appBusy.Store(false)
	dir := filepath.Join(filepath.Dir(u.path), "app")
	before, _ := os.ReadFile(filepath.Join(dir, update.AppInfo))
	got, err := update.SyncApp(context.Background(), http.DefaultClient, rel, dir, ua)
	switch {
	case err != nil:
		log.Printf("приложение для телефона из %s: %v", rel.Version, err)
	case got > 0 && !bytes.Equal(before, mustRead(filepath.Join(dir, update.AppInfo))):
		log.Printf("приложение для телефона обновлено из %s: версия %d — телефон предложит его сам", rel.Version, got)
	}
}

func mustRead(p string) []byte { b, _ := os.ReadFile(p); return b }

// close — программа закрывается: новых замен не начинать, начатую дождаться. Иначе выход
// мог бы прийтись между двумя переименованиями, и filin.exe не осталось бы вовсе.
func (u *updater) close() {
	u.mu.Lock()
	u.closing = true
	if u.stop != nil {
		u.stop() // скачивание — прервать; копию и перестановку файлов (если уже идут) — дождаться
	}
	u.mu.Unlock()
	u.applying.Wait()
}

// apply скачивает выпуск, делает копию библиотеки и ставит новую программу на место.
// Пока не дошло до Swap, рабочая программа не тронута.
func (u *updater) apply(ctx context.Context, exe string, rel update.Release, ua string) error {
	// общего предела по времени нет: на медленном канале 50 МБ идут и 20 минут, а предел в 15
	// не пустил к 1.0.3 никого (обрыв и каждый раз с нуля). Скачивание само рвёт соединение,
	// если долго нет данных, и докачивает недостающее (update.Fetch). Ход — в подписи значка.
	log.Printf("обновление до %s: скачиваю %.1f МБ", rel.Version, float64(rel.Size)/(1<<20))
	var shown atomic.Int64
	err := update.Download(ctx, http.DefaultClient, rel, exe, ua, func(done, total int64) {
		if mb := done >> 20; mb != shown.Load() {
			shown.Store(mb)
			desktop.SetTip(fmt.Sprintf("Филин: скачиваю %s — %d из %d МБ", rel.Version, mb, total>>20))
		}
	})
	desktop.SetTip("")
	if err != nil {
		return fmt.Errorf("скачивание: %w", err)
	}
	if ctx.Err() != nil { // закрыли ровно на последнем байте: скачанное лежит, поставится в следующий раз
		return ctx.Err()
	}
	copyPath, err := u.backup(rel.Version)
	if err != nil {
		os.Remove(update.NewPath(exe))
		return fmt.Errorf("копия библиотеки перед обновлением не сделалась, обновлять без неё не стал: %w", err)
	}
	log.Printf("копия библиотеки перед обновлением: %s", copyPath)
	if err := update.Swap(exe); err != nil {
		os.Remove(update.NewPath(exe))
		return err
	}
	pruneCopies(filepath.Dir(u.path), keepCopies)
	return nil
}

// copyPrefix — начало имени копии перед обновлением: filin-перед-v0.6.1-2026-11-08.zip.
const copyPrefix = "filin-перед-"

// backup — копия библиотеки рядом с базой. Пишется во временный файл и переименовывается:
// недописанная копия не должна выглядеть готовой.
func (u *updater) backup(next string) (string, error) {
	dir := filepath.Dir(u.path)
	name := filepath.Join(dir, fmt.Sprintf("%s%s-%s.zip", copyPrefix, next, time.Now().Format("2006-01-02")))
	f, err := os.CreateTemp(dir, ".копия-*.zip")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	err = backup.Write(context.Background(), f, u.d, u.covers, os.TempDir())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, name)
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// pruneCopies оставляет keep последних копий перед обновлением, старые удаляет. Только
// свои: архивы, которые человек скачал кнопкой «Копия», называются иначе и не трогаются.
func pruneCopies(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type copyFile struct {
		name string
		mod  time.Time
	}
	var cs []copyFile
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), copyPrefix) || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		if info, err := e.Info(); err == nil {
			cs = append(cs, copyFile{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].mod.After(cs[j].mod) })
	for i := keep; i < len(cs); i++ {
		if err := os.Remove(filepath.Join(dir, cs[i].name)); err != nil {
			log.Printf("старая копия не удалена: %v", err)
		} else {
			log.Printf("удалена старая копия перед обновлением: %s", cs[i].name)
		}
	}
}

// restart запускает обновлённую программу с теми же аргументами. Не запустилась (например,
// её забрал антивирус) — сказать, где лежит прежняя.
func restart() {
	exe, _ := restartExe.Load().(string)
	if exe == "" {
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Dir, _ = os.Getwd()
	cmd.Env = append(os.Environ(), restartedEnv+"=1")
	if err := cmd.Start(); err != nil {
		log.Printf("новая версия не запустилась: %v", err)
		desktop.Message("Филин — обновление", fmt.Sprintf("Новая версия поставлена, но не запустилась: %v\n\n"+
			"Прежняя версия лежит рядом: %s — переименуйте её обратно в %s, если новая не запустится и ярлыком.",
			err, update.OldPath(exe), filepath.Base(exe)))
	}
}

// restarted — эту программу запустила предыдущая версия после обновления. Пометка
// снимается сразу: дальше она не нужна и не должна уйти дочерним процессам.
func restarted() bool {
	v := os.Getenv(restartedEnv)
	os.Unsetenv(restartedEnv)
	return v != ""
}

// cleanupOld убирает прежнюю программу после обновления: прежний процесс в это время ещё
// выходит, поэтому несколько попыток в фоне.
func cleanupOld() {
	if !supported() {
		return
	}
	exe, err := exePath()
	if err != nil {
		return
	}
	if err := update.Cleanup(exe, 20, 500*time.Millisecond); err != nil {
		log.Printf("прежняя версия не убрана: %v", err)
	}
}
