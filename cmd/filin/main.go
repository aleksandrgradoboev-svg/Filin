// Команда filin — каталог домашней библиотеки.
//
//	filin                — программа: сервер, каталог в браузере, сова в трее (то же, что app)
//	filin app            — то же явно
//	filin serve          — только сервер, без значка (для разработки)
//	filin stats          — что лежит в базе
//	filin restore файл.zip — восстановить библиотеку из резервной копии (Филин закрыт)
//	filin install / uninstall — ярлыки «Филин» в «Пуске» и на рабочем столе
//	filin import-livelib файл.mhtml… — книги со страниц полок LiveLib, сохранённых вручную
//	filin version
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/bundled"
	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
	"github.com/aleksandrgradoboev-svg/filin/internal/desktop"
	"github.com/aleksandrgradoboev-svg/filin/internal/livelib"
	"github.com/aleksandrgradoboev-svg/filin/internal/update"
	"github.com/aleksandrgradoboev-svg/filin/internal/web"
)

// version ставится при сборке выпуска: -ldflags "-X main.version=v0.5.0".
var version = "dev"

var (
	tesseract *string
	port      *string
	explicit  = map[string]bool{} // флаги, заданные человеком, а не по умолчанию: их помнит ярлык
)

// route — команда и её аргументы. Без команды — программа (app). Архив копии, перетащенный
// мышью на filin.exe или на ярлык, — восстановление с вопросом окном: на сам filin.exe путь
// приходит единственным аргументом, на ярлык — после его «app».
func route(all []string) (string, []string) {
	if n := len(all); n > 0 && strings.EqualFold(filepath.Ext(all[n-1]), ".zip") && (n == 1 || n == 2 && all[0] == "app") {
		return "restore-drop", all[n-1:]
	}
	if len(all) == 0 {
		return "app", nil
	}
	return all[0], all[1:]
}

// writable — можно ли хранить библиотеку в папке. Программы привыкли класть в Program Files,
// а туда без прав администратора писать нельзя — сказать это понятно, а не ошибкой SQLite.
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".проба-записи-")
	if err != nil {
		abs, _ := filepath.Abs(dir)
		return fmt.Errorf("в папке %s нельзя хранить библиотеку: туда нельзя записывать. "+
			`Положите filin.exe в свою папку, например D:\Филин (не в Program Files)`, abs)
	}
	f.Close()
	return os.Remove(f.Name())
}

// defaultDB — где библиотека, если -db не задан: рядом с программой. Человек кладёт filin.exe
// в папку — там и живут база и фото; перенести Филина — перенести папку. Исключение —
// go run: там программа во временной папке сборки, и база — по-старому, в data\ разработчика.
func defaultDB() string {
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); !strings.Contains(strings.ToLower(dir), "go-build") {
			// прежний умолчальный адрес — data/filin.db: библиотеку, заведённую до 0.5.1
			// запуском без -db из папки программы, не подменять новой пустой
			if _, err := os.Stat(filepath.Join(dir, "filin.db")); err != nil {
				old := filepath.Join(dir, "data", "filin.db")
				if _, err := os.Stat(old); err == nil {
					return old
				}
			}
			return filepath.Join(dir, "filin.db")
		}
	}
	return filepath.Join("data", "filin.db")
}

func main() {
	desktop.AttachConsole() // выпуск собран без консоли; из терминала — печатать в него
	path := flag.String("db", "", "путь к файлу базы; пусто — filin.db рядом с программой")
	// serve: по умолчанию — только этот компьютер; адреса через запятую. Из сети пускаются только
	// привязанные телефоны, а привязка — со страницы «Телефоны», которая открывается только на
	// 127.0.0.1: его не убирать. app сам находит адреса домашней сети, ему нужен только порт.
	addr := flag.String("addr", "127.0.0.1:8080", "адреса сервера для serve, через запятую")
	port = flag.String("port", "8080", "порт программы (app)")
	tesseract = flag.String("tesseract", "", "программа распознавания титулов; пусто — найти самому (PATH, Program Files)")
	flag.StringVar(updatesURL, "updates", update.LatestURL, "где узнавать о новых выпусках (app); пусто — не проверять")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "использование: filin [-db путь] [-port 8080] [app | serve | stats | restore файл.zip | install | uninstall | import-livelib файл.mhtml… | import-livelib-genres жанры.mhtml | version]")
		flag.PrintDefaults()
	}
	flag.Parse()
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if *path == "" {
		*path = defaultDB()
	}
	if *tesseract == "" {
		*tesseract = desktop.FindTesseract()
	}
	cmd, args := route(flag.Args())
	if err := run(cmd, *path, *addr, args); err != nil {
		if cmd == "app" || cmd == "restore-drop" { // терминала нет — сказать окном
			desktop.Message("Филин не запустился", err.Error())
		}
		fmt.Fprintln(os.Stderr, "filin:", err)
		os.Exit(1)
	}
	restart() // обновление встало — база уже закрыта, порт свободен
}

func run(cmd, path, addr string, args []string) error {
	// обложки — рядом с базой: копируются и переносятся вместе с ней
	covers := filepath.Join(filepath.Dir(path), "covers")
	switch cmd {
	case "version":
		fmt.Println("Филин", version)
		return nil
	case "restore":
		_, err := restore(path, covers, args)
		return err
	case "restore-drop":
		abs, _ := filepath.Abs(path)
		if !desktop.Ask("Филин — восстановление", "Восстановить библиотеку из копии\n"+args[0]+"?\n\n"+
			"Библиотека: "+abs+"\nНынешние книги и фото не удалятся — они будут отложены в отдельную папку рядом.") {
			return nil
		}
		msg, err := restore(path, covers, args)
		if err != nil {
			return err
		}
		desktop.Message("Филин — восстановление", msg)
		return nil
	case "install":
		return install(path)
	case "uninstall":
		return uninstall()
	case "app", "serve", "stats", "import-livelib", "import-livelib-genres":
	default:
		flag.Usage()
		return fmt.Errorf("неизвестная команда %q", cmd)
	}
	if cmd == "app" && desktop.Running(*port, web.PingBody) {
		// уже запущен — второй сервер не поднимаем, просто показываем каталог
		return desktop.OpenBrowser("http://127.0.0.1:" + *port + "/")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writable(filepath.Dir(path)); err != nil {
		return err
	}
	d, err := db.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	switch cmd {
	case "app":
		return app(d, path, covers)
	case "serve":
		return serve(d, addr, covers)
	case "import-livelib":
		return importLiveLib(catalog.New(d), cover.Dir(covers), args)
	case "import-livelib-genres":
		return importGenres(catalog.New(d), args)
	}
	v, err := db.Version(d)
	if err != nil {
		return err
	}
	st, err := catalog.New(d).Stats()
	if err != nil {
		return err
	}
	fmt.Printf("Филин %s, база %s, схема %d\nизданий %d, экземпляров %d, полок %d, комплектов %d\n",
		version, path, v, st.Editions, st.Copies, st.Shelves, st.Collections)
	return nil
}

func serve(d *sql.DB, addr, covers string) error {
	opts := []web.Option{web.Tesseract(*tesseract)}
	if info, apk, ok := bundled.App(); ok {
		opts = append(opts, web.BundledApp(info, apk))
	}
	h, err := web.New(catalog.New(d), covers, opts...)
	if err != nil {
		return err
	}
	addrs := strings.Split(addr, ",")
	errs := make(chan error, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		go func() {
			log.Printf("Филин слушает http://%s", a)
			srv := &http.Server{Addr: a, Handler: h, ReadHeaderTimeout: 30 * time.Second} // как у app: медленный клиент не держит соединения вечно
			errs <- fmt.Errorf("%s: %w", a, srv.ListenAndServe())
		}()
	}
	return <-errs // упал один адрес — останавливаемся целиком, а не работаем вполовину
}

func importLiveLib(st *catalog.Store, covers cover.Dir, files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("укажите файлы .mhtml со страницами полок")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		books, err := livelib.ParseMHTML(data)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		r, err := livelib.Import(st, covers, books)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("%s: книг на странице %d — добавлено %d, уже были %d; экземпляров %d, обложек %d\n",
			filepath.Base(f), len(books), r.Added, r.Skipped, r.Copies, r.Covers)
		for _, p := range r.Problems {
			fmt.Println("  ⚠", p)
		}
	}
	return nil
}

func importGenres(st *catalog.Store, files []string) error {
	if len(files) != 1 {
		return fmt.Errorf("укажите один файл .mhtml со страницей livelib.ru/genres")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		return err
	}
	tree, err := livelib.ParseGenresMHTML(data)
	if err != nil {
		return fmt.Errorf("%s: %w", files[0], err)
	}
	n, err := st.ImportGenreTree(tree)
	if err != nil {
		return err
	}
	fmt.Printf("%s: разделов %d, жанров в справочнике %d\n", filepath.Base(files[0]), len(tree), n)
	return nil
}
