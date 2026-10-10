package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disintegration/imaging"
	"github.com/fsnotify/fsnotify"
	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version проставляется через -ldflags "-X main.Version=v0.3.0"
// при сборке релиза. По умолчанию — "dev".
var Version = "dev"

const githubRepo = "odinoki10001-pixel/media-gallery"

// ---------- типы ----------

type Media struct {
	Hash     string  `json:"hash"`
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Ext      string  `json:"ext"`
	Type     string  `json:"type"`
	Size     int64   `json:"size"`
	ModTime  int64   `json:"modTime"`
	TakenAt  int64   `json:"takenAt"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Camera   string  `json:"camera"`
	InSafe   bool    `json:"inSafe"`
	Favorite bool    `json:"favorite"`
	Rating   int     `json:"rating"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
}

type Section struct {
	Label string `json:"label"`
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Count int    `json:"count"`
}

type Album struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Count     int    `json:"count"`
	Protected bool   `json:"protected"`
	CreatedAt int64  `json:"createdAt"`
}

type SmartAlbum struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Filter string `json:"filter"`
	Count  int    `json:"count"`
}

type ScanProgress struct {
	Scanning    bool   `json:"scanning"`
	Processed   int    `json:"processed"`
	ThumbDone   int    `json:"thumbDone"`
	DepsStatus  string `json:"depsStatus"`
	DepsMessage string `json:"depsMessage"`
}

// UpdateInfo — информация о новой версии, которую отдаём на фронт.
type UpdateInfo struct {
	Available   bool   `json:"available"`
	Version     string `json:"version"`
	Current     string `json:"current"`
	URL         string `json:"url"`
	PublishedAt string `json:"publishedAt"`
	Notes       string `json:"notes"`
}

// ---------- DepManager ----------

type DepManager struct {
	binDir string
	mu     sync.Mutex

	status  atomic.Value
	message atomic.Value
}

func NewDepManager() *DepManager {
	cache, _ := os.UserCacheDir()
	binDir := filepath.Join(cache, "media-gallery", "bin")
	_ = os.MkdirAll(binDir, 0o755)

	d := &DepManager{binDir: binDir}
	d.status.Store("idle")
	d.message.Store("")
	return d
}

func (d *DepManager) Status() (string, string) {
	return d.status.Load().(string), d.message.Load().(string)
}

func (d *DepManager) setStatus(s, msg string) {
	d.status.Store(s)
	d.message.Store(msg)
}

func (d *DepManager) exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (d *DepManager) FFmpegPath() string {
	return filepath.Join(d.binDir, "ffmpeg"+d.exeSuffix())
}

func (d *DepManager) FFprobePath() string {
	return filepath.Join(d.binDir, "ffprobe"+d.exeSuffix())
}

func (d *DepManager) MagickPath() string {
	return filepath.Join(d.binDir, "magick"+d.exeSuffix())
}

func (d *DepManager) EnsureAll() {
	d.setStatus("downloading", "Подготовка инструментов…")

	needFF := !fileExists(d.FFmpegPath())
	needMagick := !fileExists(d.MagickPath())

	if !needFF && !needMagick {
		d.setStatus("ready", "Все инструменты готовы")
		time.Sleep(3 * time.Second)
		d.setStatus("idle", "")
		return
	}

	var failed []string

	if needFF {
		if err := d.ensureFFmpeg(); err != nil {
			log.Printf("[deps] ffmpeg: %v", err)
			failed = append(failed, "ffmpeg")
		}
	}
	if needMagick {
		if err := d.ensureImageMagick(); err != nil {
			log.Printf("[deps] imagemagick: %v", err)
			failed = append(failed, "ImageMagick")
		}
	}

	if len(failed) > 0 {
		d.setStatus("partial", "Не удалось скачать: "+strings.Join(failed, ", "))
	} else {
		d.setStatus("ready", "Все инструменты готовы")
	}
	time.Sleep(3 * time.Second)
	d.setStatus("idle", "")
}

func (d *DepManager) ensureFFmpeg() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("автоскачивание ffmpeg реализовано только для Windows")
	}
	url := "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"

	d.setStatus("downloading", "Скачиваю ffmpeg…")
	tmp, err := d.downloadToTemp(url, "ffmpeg", "ffmpeg")
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	d.setStatus("installing", "Распаковываю ffmpeg…")
	if err := d.extractFromZip(tmp, "ffmpeg.exe", d.FFmpegPath()); err != nil {
		return err
	}
	_ = d.extractFromZip(tmp, "ffprobe.exe", d.FFprobePath())
	return nil
}

func (d *DepManager) ensureImageMagick() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("автоскачивание ImageMagick реализовано только для Windows")
	}
	url := "https://imagemagick.org/archive/binaries/ImageMagick-7.1.1-38-portable-Q16-HDRI-x64.zip"

	d.setStatus("downloading", "Скачиваю ImageMagick…")
	tmp, err := d.downloadToTemp(url, "imagemagick", "ImageMagick")
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	d.setStatus("installing", "Распаковываю ImageMagick…")
	return d.extractAllFromZip(tmp, d.binDir, func(rel string) bool {
		base := strings.ToLower(filepath.Base(rel))
		return strings.HasSuffix(base, ".exe") || strings.HasSuffix(base, ".dll") ||
			strings.HasSuffix(base, ".xml") || strings.HasSuffix(base, ".icc")
	})
}

func (d *DepManager) downloadToTemp(url, kind, label string) (string, error) {
	tmp, err := os.CreateTemp("", "mg-"+kind+"-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	tmp.Close()

	if err := d.downloadFile(url, name, label); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func (d *DepManager) downloadFile(url, dest, label string) error {
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 64*1024)
	lastUpdate := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if time.Since(lastUpdate) > 200*time.Millisecond {
				if total > 0 {
					d.setStatus("downloading", fmt.Sprintf("%s: %s / %s",
						label, humanBytes(written), humanBytes(total)))
				} else {
					d.setStatus("downloading", fmt.Sprintf("%s: %s", label, humanBytes(written)))
				}
				lastUpdate = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}
	return nil
}

func (d *DepManager) extractFromZip(zipPath, baseName, destPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) != baseName {
			continue
		}
		return writeZipEntry(f, destPath)
	}
	return fmt.Errorf("%s не найден в архиве", baseName)
}

func (d *DepManager) extractAllFromZip(zipPath, destDir string, keep func(rel string) bool) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if keep != nil && !keep(f.Name) {
			continue
		}
		rel := filepath.Base(f.Name)
		dest := filepath.Join(destDir, rel)
		if err := writeZipEntry(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d Б", n)
	case n < k*k:
		return fmt.Sprintf("%.1f КБ", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f МБ", float64(n)/k/k)
	default:
		return fmt.Sprintf("%.2f ГБ", float64(n)/k/k/k)
	}
}

// ---------- App ----------

type App struct {
	ctx      context.Context
	db       *sql.DB
	thumbDir string
	watcher  *fsnotify.Watcher
	roots    []string
	depMgr   *DepManager

	mu       sync.Mutex
	pending  map[string]struct{}
	unlocked bool

	hasFF atomic.Bool
	hasIM atomic.Bool

	scanning  atomic.Bool
	processed atomic.Int64
	thumbDone atomic.Int64

	thumbWake chan struct{}

	// последнее известное обновление (для повторных запросов с фронта)
	lastUpdate atomic.Value
}

func NewApp() *App {
	return &App{
		pending:   map[string]struct{}{},
		thumbWake: make(chan struct{}, 1),
	}
}

// ---------- UTF-16LE для PowerShell -EncodedCommand ----------

func utf16le(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		if r < 0x10000 {
			out = append(out, byte(r), byte(r>>8))
		} else {
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out, byte(hi), byte(hi>>8))
			out = append(out, byte(lo), byte(lo>>8))
		}
	}
	return out
}

func runPowerShellScript(script string) error {
	enc := base64.StdEncoding.EncodeToString(utf16le(script))
	cmd := hiddenCmd("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", enc)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell: %v — %s", err, string(out))
	}
	return nil
}

func runPowerShellFile(body string) error {
	tmp, err := os.CreateTemp("", "mg-*.ps1")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	cmd := hiddenCmd("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", tmp.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell: %v — %s", err, string(out))
	}
	return nil
}

// ---------- жизненный цикл ----------

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	cache, _ := os.UserCacheDir()
	a.thumbDir = filepath.Join(cache, "media-gallery", "thumbs")
	_ = os.MkdirAll(a.thumbDir, 0o755)

	dataDir, _ := os.UserConfigDir()
	dbPath := filepath.Join(dataDir, "media-gallery", "index.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)

	db, err := sql.Open("sqlite", dbPath+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)")
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	a.db = db

	if err := a.initSchema(); err != nil {
		log.Fatal(err)
	}

	cols := map[string]bool{}
	if rows, err := a.db.Query(`PRAGMA table_info(albums)`); err == nil {
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
				cols[name] = true
			}
		}
		rows.Close()
	}
	need := map[string]string{
		"protected": "INTEGER NOT NULL DEFAULT 0",
		"pwd_hash":  "TEXT NOT NULL DEFAULT ''",
		"pwd_type":  "TEXT NOT NULL DEFAULT ''",
		"hint":      "TEXT NOT NULL DEFAULT ''",
	}
	for col, def := range need {
		if !cols[col] {
			if _, err := a.db.Exec("ALTER TABLE albums ADD COLUMN " + col + " " + def); err != nil {
				log.Printf("[startup] ALTER albums.%s: %v", col, err)
			}
		}
	}

	a.lastUpdate.Store(UpdateInfo{})

	a.depMgr = NewDepManager()
	a.refreshToolFlags()
	a.logToolFlags()

	// Проверка обновлений — сразу и потом раз в сутки
	go a.updatesLoop()

	go func() {
		time.Sleep(2 * time.Second)
		a.depMgr.EnsureAll()
		a.refreshToolFlags()
		a.logToolFlags()
		a.enqueueMissingThumbs()
	}()

	a.roots = a.collectRoots()

	if v := os.Getenv("MEDIA_GALLERY_DIRS"); v != "" {
		a.roots = strings.Split(v, ",")
	}

	log.Println("scan roots:")
	for _, r := range a.roots {
		log.Println("  -", r)
	}

	go a.fullScan()
	go a.startWatcher()
	go a.thumbWorker()
	go a.progressLoop()
}

// collectRoots возвращает список путей для сканирования.
func (a *App) collectRoots() []string {
	home, _ := os.UserHomeDir()
	roots := []string{home}

	if runtime.GOOS == "windows" {
		homeVol := strings.ToUpper(filepath.VolumeName(home))
		for _, letter := range "DEFGHIJKLMNOPQRSTUVWXYZ" {
			vol := string(letter) + ":"
			if strings.EqualFold(vol, homeVol) {
				continue
			}
			p := vol + "\\"
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				roots = append(roots, p)
			}
		}
	} else {
		for _, mnt := range []string{"/media", "/mnt", "/Volumes"} {
			entries, err := os.ReadDir(mnt)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					roots = append(roots, filepath.Join(mnt, e.Name()))
				}
			}
		}
	}

	return roots
}

// ---------- Обновления ----------

// updatesLoop — первый чек через 5 секунд после старта, потом раз в 24 часа.
func (a *App) updatesLoop() {
	time.Sleep(5 * time.Second)
	a.checkForUpdates()

	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for range t.C {
		a.checkForUpdates()
	}
}

// checkForUpdates запрашивает последний релиз у GitHub и, если версия новее,
// эмитит событие "update-available" на фронт.
func (a *App) checkForUpdates() {
	info, err := fetchLatestRelease()
	if err != nil {
		log.Printf("[updates] check failed: %v", err)
		return
	}
	info.Current = Version

	// Считаем "новее" только если текущая версия не dev и теги различаются.
	if Version == "dev" || !isNewer(info.Version, Version) {
		a.lastUpdate.Store(info)
		return
	}

	info.Available = true
	a.lastUpdate.Store(info)
	log.Printf("[updates] available: %s (current %s)", info.Version, Version)

	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "update-available", info)
	}
}

// fetchLatestRelease делает запрос к GitHub API и разбирает ответ.
func fetchLatestRelease() (UpdateInfo, error) {
	var info UpdateInfo

	req, err := http.NewRequest("GET",
		"https://api.github.com/repos/"+githubRepo+"/releases/latest", nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "media-gallery-updater")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("HTTP %s", resp.Status)
	}

	var body struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return info, err
	}

	info.Version = body.TagName
	info.URL = body.HTMLURL
	info.PublishedAt = body.PublishedAt
	info.Notes = body.Body
	return info, nil
}

// isNewer сравнивает две semver-подобные версии: "v0.3.0" > "v0.2.0".
// Префикс "v" игнорируется, отсутствующие части считаются нулями.
func isNewer(a, b string) bool {
	av := parseVersion(a)
	bv := parseVersion(b)
	for i := 0; i < 3; i++ {
		if av[i] > bv[i] {
			return true
		}
		if av[i] < bv[i] {
			return false
		}
	}
	return false
}

func parseVersion(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// отрезаем возможные суффиксы "-beta" и т.п.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}

// GetUpdateInfo возвращает текущее состояние проверки для фронта.
func (a *App) GetUpdateInfo() UpdateInfo {
	if v := a.lastUpdate.Load(); v != nil {
		if u, ok := v.(UpdateInfo); ok {
			u.Current = Version
			return u
		}
	}
	return UpdateInfo{Current: Version}
}

// CheckForUpdates — ручной чек (кнопка «Проверить обновления»).
func (a *App) CheckForUpdates() UpdateInfo {
	a.checkForUpdates()
	return a.GetUpdateInfo()
}

// GetVersion возвращает строку версии текущего билда.
func (a *App) GetVersion() string {
	return Version
}

// OpenURL открывает ссылку в системном браузере.
func (a *App) OpenURL(url string) error {
	if url == "" {
		return fmt.Errorf("пустой URL")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("разрешены только http/https")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = hiddenCmd("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = hiddenCmd("open", url)
	default:
		cmd = hiddenCmd("xdg-open", url)
	}
	return cmd.Start()
}

func (a *App) refreshToolFlags() {
	a.hasFF.Store(a.toolPath("ffmpeg", a.depMgr.FFmpegPath()) != "")
	a.hasIM.Store(a.toolPath("magick", a.depMgr.MagickPath()) != "" ||
		a.toolPath("convert", "") != "")
}

func (a *App) toolPath(name, localPath string) string {
	if localPath != "" && fileExists(localPath) {
		return localPath
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func (a *App) ffmpegCmd(args ...string) *exec.Cmd {
	return hiddenCmd(a.toolPath("ffmpeg", a.depMgr.FFmpegPath()), args...)
}

func (a *App) magickCmd(args ...string) *exec.Cmd {
	if a.depMgr != nil && fileExists(a.depMgr.MagickPath()) {
		return hiddenCmd(a.depMgr.MagickPath(), args...)
	}
	if p, err := exec.LookPath("magick"); err == nil {
		return hiddenCmd(p, args...)
	}
	return hiddenCmd("convert", args...)
}

func (a *App) logToolFlags() {
	if !a.hasFF.Load() {
		log.Println("⚠ ffmpeg недоступен")
	} else {
		log.Println("✓ ffmpeg готов")
	}
	if !a.hasIM.Load() {
		log.Println("⚠ ImageMagick недоступен")
	} else {
		log.Println("✓ ImageMagick готов")
	}
}

func (a *App) shutdown(ctx context.Context) {
	if a.watcher != nil {
		_ = a.watcher.Close()
	}
	if a.db != nil {
		_ = a.db.Close()
	}
}

func (a *App) initSchema() error {
	_, err := a.db.Exec(`
		CREATE TABLE IF NOT EXISTS media (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			hash         TEXT UNIQUE NOT NULL,
			content_hash TEXT NOT NULL DEFAULT '',
			path         TEXT UNIQUE NOT NULL,
			path_lower   TEXT NOT NULL DEFAULT '',
			name         TEXT NOT NULL,
			name_lower   TEXT NOT NULL DEFAULT '',
			ext          TEXT NOT NULL,
			type         TEXT NOT NULL,
			size         INTEGER NOT NULL,
			mod_time     INTEGER NOT NULL,
			taken_at     INTEGER NOT NULL DEFAULT 0,
			width        INTEGER NOT NULL DEFAULT 0,
			height       INTEGER NOT NULL DEFAULT 0,
			camera       TEXT NOT NULL DEFAULT '',
			in_safe      INTEGER NOT NULL DEFAULT 0,
			favorite     INTEGER NOT NULL DEFAULT 0,
			rating       INTEGER NOT NULL DEFAULT 0,
			lat          REAL NOT NULL DEFAULT 0,
			lon          REAL NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS albums (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			protected  INTEGER NOT NULL DEFAULT 0,
			pwd_hash   TEXT NOT NULL DEFAULT '',
			pwd_type   TEXT NOT NULL DEFAULT '',
			hint       TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS album_items (
			album_id   INTEGER NOT NULL,
			media_hash TEXT NOT NULL,
			added_at   INTEGER NOT NULL,
			PRIMARY KEY (album_id, media_hash)
		);
		CREATE TABLE IF NOT EXISTS smart_albums (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT NOT NULL,
			filter     TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	for _, c := range []struct{ name, def string }{
		{"content_hash", "TEXT NOT NULL DEFAULT ''"},
		{"path_lower", "TEXT NOT NULL DEFAULT ''"},
		{"name_lower", "TEXT NOT NULL DEFAULT ''"},
		{"taken_at", "INTEGER NOT NULL DEFAULT 0"},
		{"width", "INTEGER NOT NULL DEFAULT 0"},
		{"height", "INTEGER NOT NULL DEFAULT 0"},
		{"camera", "TEXT NOT NULL DEFAULT ''"},
		{"in_safe", "INTEGER NOT NULL DEFAULT 0"},
		{"favorite", "INTEGER NOT NULL DEFAULT 0"},
		{"rating", "INTEGER NOT NULL DEFAULT 0"},
		{"lat", "REAL NOT NULL DEFAULT 0"},
		{"lon", "REAL NOT NULL DEFAULT 0"},
	} {
		_, _ = a.db.Exec("ALTER TABLE media ADD COLUMN " + c.name + " " + c.def)
	}
	for _, c := range []struct{ name, def string }{
		{"protected", "INTEGER NOT NULL DEFAULT 0"},
		{"pwd_hash", "TEXT NOT NULL DEFAULT ''"},
		{"pwd_type", "TEXT NOT NULL DEFAULT ''"},
		{"hint", "TEXT NOT NULL DEFAULT ''"},
	} {
		_, _ = a.db.Exec("ALTER TABLE albums ADD COLUMN " + c.name + " " + c.def)
	}
	_, _ = a.db.Exec(`UPDATE media SET taken_at = mod_time WHERE taken_at = 0`)
	_, _ = a.db.Exec(`UPDATE media SET name_lower = LOWER(name) WHERE name_lower = ''`)
	_, _ = a.db.Exec(`UPDATE media SET path_lower = LOWER(path) WHERE path_lower = ''`)

	_, _ = a.db.Exec(`
		DROP TRIGGER IF EXISTS media_ai;
		DROP TRIGGER IF EXISTS media_ad;
		DROP TRIGGER IF EXISTS media_au;
		DROP TABLE IF EXISTS media_fts;
	`)
	_, _ = a.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS media_fts USING fts5(
			name, path, camera,
			content='media', content_rowid='id',
			tokenize='unicode61 remove_diacritics 2'
		);
		CREATE TRIGGER IF NOT EXISTS media_ai AFTER INSERT ON media BEGIN
			INSERT INTO media_fts(rowid, name, path, camera)
			VALUES (new.id, new.name, new.path, new.camera);
		END;
		CREATE TRIGGER IF NOT EXISTS media_ad AFTER DELETE ON media BEGIN
			INSERT INTO media_fts(media_fts, rowid, name, path, camera)
			VALUES ('delete', old.id, old.name, old.path, old.camera);
		END;
		CREATE TRIGGER IF NOT EXISTS media_au AFTER UPDATE ON media BEGIN
			INSERT INTO media_fts(media_fts, rowid, name, path, camera)
			VALUES ('delete', old.id, old.name, old.path, old.camera);
			INSERT INTO media_fts(rowid, name, path, camera)
			VALUES (new.id, new.name, new.path, new.camera);
		END;
	`)
	_, _ = a.db.Exec(`INSERT INTO media_fts(media_fts) VALUES('rebuild')`)

	_, err = a.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_media_taken_at     ON media(taken_at DESC);
		CREATE INDEX IF NOT EXISTS idx_media_content_hash ON media(content_hash);
		CREATE INDEX IF NOT EXISTS idx_media_type         ON media(type);
		CREATE INDEX IF NOT EXISTS idx_media_name_lower   ON media(name_lower);
		CREATE INDEX IF NOT EXISTS idx_media_in_safe      ON media(in_safe);
		CREATE INDEX IF NOT EXISTS idx_media_favorite     ON media(favorite);
		CREATE INDEX IF NOT EXISTS idx_media_rating       ON media(rating);
		CREATE INDEX IF NOT EXISTS idx_media_gps          ON media(lat, lon);
		CREATE INDEX IF NOT EXISTS idx_album_items_album  ON album_items(album_id);
	`)
	return err
}

// ---------- форматы ----------

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
}
var heicExts = map[string]bool{".heic": true, ".heif": true, ".avif": true}
var rawExts = map[string]bool{
	".cr2": true, ".cr3": true, ".nef": true, ".arw": true, ".dng": true,
	".raf": true, ".orf": true, ".rw2": true, ".pef": true, ".srw": true,
	".3fr": true, ".mrw": true,
}
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true, ".webm": true,
	".m4v": true, ".wmv": true, ".flv": true, ".mpeg": true, ".mpg": true,
	".3gp": true, ".m2ts": true, ".mts": true,
}

var skipDirs = map[string]bool{
	"node_modules": true, "AppData": true, "Windows": true,
	"$Recycle.Bin": true, "$RECYCLE.BIN": true, ".git": true,
	"Library": true, "System": true, ".cache": true,
	"Program Files": true, "Program Files (x86)": true, "ProgramData": true,
	"Windows.old": true, ".thumbnails": true,
	"System Volume Information": true, "Recovery": true,
	"PerfLogs": true, "$WinREAgent": true,
	"Users": true,
}

func classify(ext string) string {
	switch {
	case imageExts[ext], heicExts[ext], rawExts[ext]:
		return "image"
	case videoExts[ext]:
		return "video"
	}
	return ""
}

func looksLikeMedia(path, typ string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 16)
	n, _ := io.ReadFull(f, buf)
	if n < 8 {
		return false
	}
	buf = buf[:n]
	switch typ {
	case "image":
		if buf[0] == 0xFF && buf[1] == 0xD8 && buf[2] == 0xFF {
			return true
		}
		if bytes.HasPrefix(buf, []byte("\x89PNG\r\n\x1a\n")) {
			return true
		}
		if bytes.HasPrefix(buf, []byte("GIF87a")) || bytes.HasPrefix(buf, []byte("GIF89a")) {
			return true
		}
		if bytes.HasPrefix(buf, []byte("BM")) {
			return true
		}
		if bytes.HasPrefix(buf, []byte("II*\x00")) || bytes.HasPrefix(buf, []byte("MM\x00*")) {
			return true
		}
		if len(buf) >= 12 && bytes.Equal(buf[0:4], []byte("RIFF")) &&
			bytes.Equal(buf[8:12], []byte("WEBP")) {
			return true
		}
		if len(buf) >= 8 && bytes.Equal(buf[4:8], []byte("ftyp")) {
			return true
		}
		return !isText(buf)
	case "video":
		if len(buf) >= 8 && bytes.Equal(buf[4:8], []byte("ftyp")) {
			return true
		}
		if bytes.HasPrefix(buf, []byte("\x1A\x45\xDF\xA3")) {
			return true
		}
		if len(buf) >= 12 && bytes.Equal(buf[0:4], []byte("RIFF")) &&
			bytes.Equal(buf[8:12], []byte("AVI ")) {
			return true
		}
		if bytes.HasPrefix(buf, []byte("\x30\x26\xB2\x75")) {
			return true
		}
		if bytes.HasPrefix(buf, []byte("FLV\x01")) {
			return true
		}
		if buf[0] == 0x47 {
			return true
		}
		if bytes.HasPrefix(buf, []byte("\x00\x00\x01\xBA")) ||
			bytes.HasPrefix(buf, []byte("\x00\x00\x01\xB3")) {
			return true
		}
	}
	return false
}

func isText(buf []byte) bool {
	printable := 0
	for _, b := range buf {
		if (b >= 0x20 && b < 0x7F) || b == '\t' || b == '\n' || b == '\r' {
			printable++
		}
	}
	return printable >= len(buf)*8/10
}

func contentHashOf(path string, size int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha1.New()
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, buf)
	h.Write(buf[:n])
	fmt.Fprintf(h, "|%d", size)
	return hex.EncodeToString(h.Sum(nil))
}

func readEXIF(path string) (takenAt int64, w, h int, camera string, lat, lon float64) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return
	}
	if t, err := x.DateTime(); err == nil && !t.IsZero() {
		ts := t.Unix()
		if ts > 315532800 && ts < time.Now().Unix()+86400 {
			takenAt = ts
		}
	}
	if tag, err := x.Get(exif.PixelXDimension); err == nil {
		if v, err := tag.Int(0); err == nil {
			w = v
		}
	}
	if tag, err := x.Get(exif.PixelYDimension); err == nil {
		if v, err := tag.Int(0); err == nil {
			h = v
		}
	}
	var mk, md string
	if tag, err := x.Get(exif.Make); err == nil {
		mk, _ = tag.StringVal()
	}
	if tag, err := x.Get(exif.Model); err == nil {
		md, _ = tag.StringVal()
	}
	camera = strings.TrimSpace(mk + " " + md)
	if la, lo, err := x.LatLong(); err == nil {
		if !math.IsNaN(la) && !math.IsInf(la, 0) &&
			!math.IsNaN(lo) && !math.IsInf(lo, 0) {
			lat, lon = la, lo
		}
	}
	return
}

func safeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// ---------- сканирование ----------

func (a *App) fullScan() {
	a.scanning.Store(true)
	defer a.scanning.Store(false)

	a.processed.Store(0)
	a.thumbDone.Store(0)

	for _, root := range a.roots {
		log.Println("scanning:", root)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || skipDirs[name] {
					return filepath.SkipDir
				}
				return nil
			}
			if classify(strings.ToLower(filepath.Ext(path))) == "" {
				return nil
			}
			a.indexFile(path, d)
			a.processed.Add(1)
			return nil
		})
	}
	log.Println("full scan done, processed:", a.processed.Load())
}

func (a *App) indexFile(path string, d fs.DirEntry) {
	ext := strings.ToLower(filepath.Ext(path))
	typ := classify(ext)
	if typ == "" {
		return
	}
	info, err := d.Info()
	if err != nil {
		return
	}
	if existing, ok := a.getByPath(path); ok {
		if existing.Size == info.Size() && existing.ModTime == info.ModTime().Unix() {
			return
		}
	}
	if !looksLikeMedia(path, typ) {
		a.removeByPath(path)
		return
	}
	h := sha1.Sum([]byte(path))
	pathHash := hex.EncodeToString(h[:])
	takenAt := info.ModTime().Unix()
	w, hh, cam := 0, 0, ""
	var lat, lon float64
	if typ == "image" && imageExts[ext] {
		if t, ww, hhh, cc, la, lo := readEXIF(path); t > 0 || ww > 0 || cc != "" {
			if t > 0 {
				takenAt = t
			}
			w, hh, cam, lat, lon = ww, hhh, cc, la, lo
		}
	}
	m := Media{
		Hash: pathHash, Path: path, Name: d.Name(), Ext: ext, Type: typ,
		Size: info.Size(), ModTime: info.ModTime().Unix(), TakenAt: takenAt,
		Width: w, Height: hh, Camera: cam, Lat: lat, Lon: lon,
	}
	chash := contentHashOf(path, info.Size())
	a.upsert(m, chash)
}

func (a *App) upsert(m Media, contentHash string) {
	_, err := a.db.Exec(`
		INSERT INTO media(hash, content_hash, path, path_lower, name, name_lower,
		                  ext, type, size, mod_time, taken_at, width, height, camera, lat, lon)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash, content_hash=excluded.content_hash,
			path_lower=excluded.path_lower, name=excluded.name, name_lower=excluded.name_lower,
			ext=excluded.ext, type=excluded.type, size=excluded.size,
			mod_time=excluded.mod_time, taken_at=excluded.taken_at,
			width=excluded.width, height=excluded.height, camera=excluded.camera,
			lat=excluded.lat, lon=excluded.lon
	`, m.Hash, contentHash, m.Path, strings.ToLower(m.Path),
		m.Name, strings.ToLower(m.Name), m.Ext, m.Type, m.Size, m.ModTime,
		m.TakenAt, m.Width, m.Height, m.Camera,
		safeFloat(m.Lat), safeFloat(m.Lon))
	if err != nil {
		log.Println("upsert:", err)
	}
}

func (a *App) removeByPath(path string) {
	row := a.db.QueryRow(`SELECT hash FROM media WHERE path = ?`, path)
	var hash string
	if err := row.Scan(&hash); err == nil {
		_, _ = a.db.Exec(`DELETE FROM album_items WHERE media_hash = ?`, hash)
	}
	_, _ = a.db.Exec(`DELETE FROM media WHERE path = ?`, path)
}

const mediaCols = `hash, path, name, ext, type, size, mod_time, taken_at, width, height, camera, in_safe, favorite, rating, lat, lon`

func scanMedia(rows *sql.Rows) []Media {
	out := make([]Media, 0)
	for rows.Next() {
		var m Media
		var inSafe, fav, rating int
		if err := rows.Scan(&m.Hash, &m.Path, &m.Name, &m.Ext, &m.Type,
			&m.Size, &m.ModTime, &m.TakenAt, &m.Width, &m.Height, &m.Camera,
			&inSafe, &fav, &rating, &m.Lat, &m.Lon); err != nil {
			continue
		}
		m.InSafe = inSafe != 0
		m.Favorite = fav != 0
		m.Rating = rating
		out = append(out, m)
	}
	return out
}

func (a *App) getByHash(hash string) (Media, bool) {
	row := a.db.QueryRow(`SELECT `+mediaCols+` FROM media WHERE hash = ?`, hash)
	var m Media
	var inSafe, fav, rating int
	if err := row.Scan(&m.Hash, &m.Path, &m.Name, &m.Ext, &m.Type,
		&m.Size, &m.ModTime, &m.TakenAt, &m.Width, &m.Height, &m.Camera,
		&inSafe, &fav, &rating, &m.Lat, &m.Lon); err != nil {
		return Media{}, false
	}
	m.InSafe = inSafe != 0
	m.Favorite = fav != 0
	m.Rating = rating
	return m, true
}

func (a *App) getByPath(path string) (Media, bool) {
	row := a.db.QueryRow(`SELECT `+mediaCols+` FROM media WHERE path = ?`, path)
	var m Media
	var inSafe, fav, rating int
	if err := row.Scan(&m.Hash, &m.Path, &m.Name, &m.Ext, &m.Type,
		&m.Size, &m.ModTime, &m.TakenAt, &m.Width, &m.Height, &m.Camera,
		&inSafe, &fav, &rating, &m.Lat, &m.Lon); err != nil {
		return Media{}, false
	}
	m.InSafe = inSafe != 0
	m.Favorite = fav != 0
	m.Rating = rating
	return m, true
}

// ---------- fsnotify ----------

func (a *App) startWatcher() {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("fsnotify:", err)
		return
	}
	a.watcher = w
	for _, root := range a.roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || skipDirs[name] {
					return filepath.SkipDir
				}
				_ = w.Add(path)
			}
			return nil
		})
	}
	go a.watchLoop()
	go a.flushLoop()
}

func (a *App) watchLoop() {
	for {
		select {
		case ev, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			a.handleEvent(ev)
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			log.Println("watcher error:", err)
		}
	}
}

func (a *App) handleEvent(ev fsnotify.Event) {
	switch {
	case ev.Op.Has(fsnotify.Create):
		info, err := os.Stat(ev.Name)
		if err != nil {
			return
		}
		if info.IsDir() {
			_ = filepath.WalkDir(ev.Name, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					name := d.Name()
					if strings.HasPrefix(name, ".") || skipDirs[name] {
						return filepath.SkipDir
					}
					_ = a.watcher.Add(path)
				}
				return nil
			})
			_ = filepath.WalkDir(ev.Name, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() {
					a.indexFile(path, d)
				}
				return nil
			})
			return
		}
		a.queue(ev.Name)
	case ev.Op.Has(fsnotify.Write):
		a.queue(ev.Name)
	case ev.Op.Has(fsnotify.Remove), ev.Op.Has(fsnotify.Rename):
		a.removeByPath(ev.Name)
	}
}

func (a *App) queue(path string) {
	if classify(strings.ToLower(filepath.Ext(path))) == "" {
		return
	}
	a.mu.Lock()
	a.pending[path] = struct{}{}
	a.mu.Unlock()
}

func (a *App) flushLoop() {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		a.mu.Lock()
		if len(a.pending) == 0 {
			a.mu.Unlock()
			continue
		}
		batch := make([]string, 0, len(a.pending))
		for p := range a.pending {
			batch = append(batch, p)
		}
		a.pending = map[string]struct{}{}
		a.mu.Unlock()
		for _, p := range batch {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				a.indexFile(p, fs.FileInfoToDirEntry(info))
				select {
				case a.thumbWake <- struct{}{}:
				default:
				}
			}
		}
	}
}

// ---------- воркер превью ----------

func (a *App) enqueueMissingThumbs() {
	select {
	case a.thumbWake <- struct{}{}:
	default:
	}
}

func (a *App) thumbWorker() {
	for {
		select {
		case <-a.thumbWake:
		case <-time.After(5 * time.Second):
		}
		rows, err := a.db.Query(`SELECT ` + mediaCols + ` FROM media WHERE in_safe = 0 LIMIT 5000`)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var list []Media
		for rows.Next() {
			var m Media
			var inSafe, fav, rating int
			if err := rows.Scan(&m.Hash, &m.Path, &m.Name, &m.Ext, &m.Type,
				&m.Size, &m.ModTime, &m.TakenAt, &m.Width, &m.Height, &m.Camera,
				&inSafe, &fav, &rating, &m.Lat, &m.Lon); err != nil {
				continue
			}
			list = append(list, m)
		}
		rows.Close()
		for _, m := range list {
			p := filepath.Join(a.thumbDir, m.Hash+".jpg")
			if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
				continue
			}
			if _, err := a.generateThumb(m); err == nil {
				a.thumbDone.Add(1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// ---------- прогресс ----------

func (a *App) progressLoop() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if a.ctx == nil {
			continue
		}
		ds, dm := "idle", ""
		if a.depMgr != nil {
			ds, dm = a.depMgr.Status()
		}
		wailsruntime.EventsEmit(a.ctx, "progress", ScanProgress{
			Scanning:    a.scanning.Load(),
			Processed:   int(a.processed.Load()),
			ThumbDone:   int(a.thumbDone.Load()),
			DepsStatus:  ds,
			DepsMessage: dm,
		})
	}
}

func (a *App) GetProgress() ScanProgress {
	ds, dm := "idle", ""
	if a.depMgr != nil {
		ds, dm = a.depMgr.Status()
	}
	return ScanProgress{
		Scanning:    a.scanning.Load(),
		Processed:   int(a.processed.Load()),
		ThumbDone:   int(a.thumbDone.Load()),
		DepsStatus:  ds,
		DepsMessage: dm,
	}
}

// ---------- превью ----------

var thumbSem = make(chan struct{}, 4)

const thumbSize = 320

func (a *App) generateThumb(m Media) (string, error) {
	out := filepath.Join(a.thumbDir, m.Hash+".jpg")
	if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
		return out, nil
	}
	_ = os.MkdirAll(a.thumbDir, 0o755)
	ext := strings.ToLower(m.Ext)

	if imageExts[ext] {
		if img, err := imaging.Open(m.Path, imaging.AutoOrientation(true)); err == nil {
			th := imaging.Fill(img, thumbSize, thumbSize, imaging.Center, imaging.Lanczos)
			if err := imaging.Save(th, out, imaging.JPEGQuality(80)); err == nil {
				return out, nil
			}
		}
	}
	if a.hasFF.Load() {
		vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d",
			thumbSize, thumbSize, thumbSize, thumbSize)
		var args []string
		if m.Type == "video" {
			args = []string{"-y", "-ss", "0.5", "-i", m.Path, "-vf", vf, "-frames:v", "1", "-q:v", "3", out}
		} else {
			args = []string{"-y", "-i", m.Path, "-vf", vf, "-frames:v", "1", "-q:v", "3", out}
		}
		if err := a.ffmpegCmd(args...).Run(); err == nil {
			if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
				return out, nil
			}
		}
	}
	if a.hasIM.Load() {
		args := []string{m.Path + "[0]",
			"-resize", fmt.Sprintf("%dx%d^", thumbSize, thumbSize),
			"-gravity", "center", "-extent", fmt.Sprintf("%dx%d", thumbSize, thumbSize),
			"-quality", "80", out}
		if err := a.magickCmd(args...).Run(); err == nil {
			if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
				return out, nil
			}
		}
	}
	return "", fmt.Errorf("cannot generate preview")
}

func (a *App) handleThumb(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/thumb/")
	if i := strings.IndexByte(hash, '.'); i >= 0 {
		hash = hash[:i]
	}
	m, ok := a.getByHash(hash)
	if !ok {
		http.NotFound(w, r)
		return
	}
	thumbSem <- struct{}{}
	defer func() { <-thumbSem }()
	p, err := a.generateThumb(m)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, p)
}

func (a *App) handleFile(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/file/")
	m, ok := a.getByHash(hash)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if m.InSafe && !a.unlocked {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, m.Path)
}

// ---------- поиск ----------

func ftsQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	parts := strings.Fields(q)
	for i, p := range parts {
		p = strings.ReplaceAll(p, `"`, `""`)
		parts[i] = `"` + p + `"*`
	}
	return strings.Join(parts, " AND ")
}

func (a *App) Search(query, typ string, limit, offset int) []Media {
	if limit <= 0 {
		limit = 200
	}
	q := strings.TrimSpace(query)

	if q == "" {
		rows, err := a.db.Query(`
			SELECT `+mediaCols+` FROM media
			WHERE (? = '' OR type = ?) AND in_safe = 0
			ORDER BY taken_at DESC LIMIT ? OFFSET ?`, typ, typ, limit, offset)
		if err != nil {
			return []Media{}
		}
		defer rows.Close()
		return scanMedia(rows)
	}

	if fq := ftsQuery(q); fq != "" {
		rows, err := a.db.Query(`
			SELECT m.`+strings.ReplaceAll(mediaCols, ", ", ", m.")+`
			FROM media m
			JOIN media_fts f ON f.rowid = m.id
			WHERE media_fts MATCH ? AND (? = '' OR m.type = ?) AND m.in_safe = 0
			ORDER BY m.taken_at DESC LIMIT ? OFFSET ?`,
			fq, typ, typ, limit, offset)
		if err == nil {
			out := scanMedia(rows)
			rows.Close()
			if len(out) > 0 {
				return out
			}
		} else if rows != nil {
			rows.Close()
		}
	}

	lq := "%" + strings.ToLower(q) + "%"
	rows, err := a.db.Query(`
		SELECT `+mediaCols+` FROM media
		WHERE (? = '' OR type = ?) AND in_safe = 0
		  AND (name_lower LIKE ? OR path_lower LIKE ? OR LOWER(camera) LIKE ?)
		ORDER BY taken_at DESC LIMIT ? OFFSET ?`,
		typ, typ, lq, lq, lq, limit, offset)
	if err != nil {
		log.Println("Search LIKE:", err)
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

func (a *App) TotalCount(query, typ string) int {
	q := strings.TrimSpace(query)
	var n int
	if q == "" {
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM media WHERE (?='' OR type=?) AND in_safe=0`,
			typ, typ).Scan(&n)
		return n
	}
	if fq := ftsQuery(q); fq != "" {
		err := a.db.QueryRow(`
			SELECT COUNT(*) FROM media m
			JOIN media_fts f ON f.rowid = m.id
			WHERE media_fts MATCH ? AND (?='' OR m.type=?) AND m.in_safe=0`,
			fq, typ, typ).Scan(&n)
		if err == nil && n > 0 {
			return n
		}
	}
	lq := "%" + strings.ToLower(q) + "%"
	_ = a.db.QueryRow(`
		SELECT COUNT(*) FROM media
		WHERE (?='' OR type=?) AND in_safe=0
		  AND (name_lower LIKE ? OR path_lower LIKE ? OR LOWER(camera) LIKE ?)`,
		typ, typ, lq, lq, lq).Scan(&n)
	return n
}

// ---------- секции по датам ----------

var monthsRu = [...]string{
	"Январь", "Февраль", "Март", "Апрель", "Май", "Июнь",
	"Июль", "Август", "Сентябрь", "Октябрь", "Ноябрь", "Декабрь",
}

func (a *App) GetSections(typ string) []Section {
	now := time.Now()
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()
	yesterdayStart := todayStart - 86400
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	weekStart := todayStart - int64((wd-1)*86400)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
	lastMonthStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, loc).Unix()

	var sections []Section
	count := func(from, to int64) int {
		var n int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM media
			WHERE (?='' OR type=?) AND in_safe=0 AND taken_at>=? AND taken_at<?`,
			typ, typ, from, to).Scan(&n)
		return n
	}
	add := func(label string, from, to int64) {
		if n := count(from, to); n > 0 {
			sections = append(sections, Section{Label: label, From: from, To: to, Count: n})
		}
	}
	add("Сегодня", todayStart, todayStart+86400)
	add("Вчера", yesterdayStart, todayStart)
	if weekStart < yesterdayStart {
		add("На этой неделе", weekStart, yesterdayStart)
	}
	if monthStart < weekStart {
		add("В этом месяце", monthStart, weekStart)
	}
	if lastMonthStart < monthStart {
		add("В прошлом месяце", lastMonthStart, monthStart)
	}

	rows, err := a.db.Query(`
		SELECT strftime('%Y-%m', taken_at, 'unixepoch', 'localtime') AS ym,
		       MIN(taken_at), MAX(taken_at)+1, COUNT(*)
		FROM media WHERE (?='' OR type=?) AND in_safe=0 AND taken_at<?
		GROUP BY ym ORDER BY ym DESC`, typ, typ, lastMonthStart)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ym string
			var from, to int64
			var cnt int
			if err := rows.Scan(&ym, &from, &to, &cnt); err != nil {
				continue
			}
			p := strings.Split(ym, "-")
			if len(p) != 2 {
				continue
			}
			y, _ := strconv.Atoi(p[0])
			mo, _ := strconv.Atoi(p[1])
			if mo < 1 || mo > 12 {
				continue
			}
			sections = append(sections, Section{
				Label: monthsRu[mo-1] + " " + strconv.Itoa(y),
				From:  from, To: to, Count: cnt,
			})
		}
	}
	return sections
}

func (a *App) GetSectionItems(from, to int64, typ string, offset, limit int) []Media {
	if limit <= 0 {
		limit = 100
	}
	rows, err := a.db.Query(`SELECT `+mediaCols+` FROM media
		WHERE (?='' OR type=?) AND in_safe=0 AND taken_at>=? AND taken_at<?
		ORDER BY taken_at DESC LIMIT ? OFFSET ?`, typ, typ, from, to, limit, offset)
	if err != nil {
		log.Println(err)
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

// ---------- избранное / рейтинг ----------

func (a *App) ToggleFavorite(hash string) (bool, error) {
	var cur int
	if err := a.db.QueryRow(`SELECT favorite FROM media WHERE hash=?`, hash).Scan(&cur); err != nil {
		return false, err
	}
	nv := 0
	if cur == 0 {
		nv = 1
	}
	_, err := a.db.Exec(`UPDATE media SET favorite=? WHERE hash=?`, nv, hash)
	return nv == 1, err
}

func (a *App) SetRating(hash string, rating int) error {
	if rating < 0 {
		rating = 0
	}
	if rating > 5 {
		rating = 5
	}
	_, err := a.db.Exec(`UPDATE media SET rating=? WHERE hash=?`, rating, hash)
	return err
}

func (a *App) Favorites(typ string, offset, limit int) []Media {
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.db.Query(`SELECT `+mediaCols+` FROM media
		WHERE favorite=1 AND (?='' OR type=?) AND in_safe=0
		ORDER BY taken_at DESC LIMIT ? OFFSET ?`, typ, typ, limit, offset)
	if err != nil {
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

func (a *App) FavoritesCount(typ string) int {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM media WHERE favorite=1 AND (?='' OR type=?) AND in_safe=0`,
		typ, typ).Scan(&n)
	return n
}

func (a *App) RatedItems(minRating int, typ string, offset, limit int) []Media {
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.db.Query(`
		SELECT `+mediaCols+` FROM media
		WHERE rating >= ? AND (?='' OR type=?) AND in_safe=0
		ORDER BY rating DESC, taken_at DESC LIMIT ? OFFSET ?`,
		minRating, typ, typ, limit, offset)
	if err != nil {
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

func (a *App) RatedCount(minRating int, typ string) int {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM media WHERE rating>=? AND (?='' OR type=?) AND in_safe=0`,
		minRating, typ, typ).Scan(&n)
	return n
}

func (a *App) GeoItems(offset, limit int) []Media {
	if limit <= 0 {
		limit = 2000
	}
	rows, err := a.db.Query(`SELECT `+mediaCols+` FROM media
		WHERE (lat != 0 OR lon != 0) AND in_safe = 0
		ORDER BY taken_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

// ---------- альбомы ----------

func (a *App) ListAlbums() []Album {
	rows, err := a.db.Query(`
		SELECT a.id, a.name, a.created_at, a.protected,
		       (SELECT COUNT(*) FROM album_items ai WHERE ai.album_id = a.id)
		FROM albums a ORDER BY a.created_at DESC`)
	if err != nil {
		return []Album{}
	}
	defer rows.Close()
	out := make([]Album, 0)
	for rows.Next() {
		var al Album
		var prot int
		if err := rows.Scan(&al.ID, &al.Name, &al.CreatedAt, &prot, &al.Count); err != nil {
			continue
		}
		al.Protected = prot != 0
		out = append(out, al)
	}
	return out
}

func (a *App) CreateAlbum(name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("пустое имя")
	}
	res, err := a.db.Exec(`INSERT INTO albums(name, created_at) VALUES(?, ?)`, name, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (a *App) RenameAlbum(id int64, name string) error {
	_, err := a.db.Exec(`UPDATE albums SET name=? WHERE id=?`, name, id)
	return err
}

func (a *App) DeleteAlbum(id int64) error {
	_, _ = a.db.Exec(`DELETE FROM album_items WHERE album_id=?`, id)
	_, err := a.db.Exec(`DELETE FROM albums WHERE id=?`, id)
	return err
}

func (a *App) AddToAlbum(albumID int64, hash string) error {
	_, err := a.db.Exec(`INSERT OR IGNORE INTO album_items(album_id, media_hash, added_at) VALUES(?, ?, ?)`,
		albumID, hash, time.Now().Unix())
	return err
}

func (a *App) AddManyToAlbum(albumID int64, hashes []string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO album_items(album_id, media_hash, added_at) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().Unix()
	for _, h := range hashes {
		_, _ = stmt.Exec(albumID, h, now)
	}
	return tx.Commit()
}

func (a *App) RemoveFromAlbum(albumID int64, hash string) error {
	_, err := a.db.Exec(`DELETE FROM album_items WHERE album_id=? AND media_hash=?`, albumID, hash)
	return err
}

func (a *App) AlbumItems(albumID int64, offset, limit int) []Media {
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.db.Query(`SELECT `+mediaCols+` FROM media m
		JOIN album_items ai ON ai.media_hash = m.hash
		WHERE ai.album_id=?
		ORDER BY ai.added_at DESC, m.taken_at DESC LIMIT ? OFFSET ?`,
		albumID, limit, offset)
	if err != nil {
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

func (a *App) AlbumCount(albumID int64) int {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM album_items WHERE album_id=?`, albumID).Scan(&n)
	return n
}

// ---------- умные альбомы ----------

func (a *App) ListSmartAlbums() []SmartAlbum {
	rows, err := a.db.Query(`SELECT id, name, filter FROM smart_albums ORDER BY id DESC`)
	if err != nil {
		return []SmartAlbum{}
	}
	defer rows.Close()
	out := make([]SmartAlbum, 0)
	for rows.Next() {
		var s SmartAlbum
		if err := rows.Scan(&s.ID, &s.Name, &s.Filter); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (a *App) CreateSmartAlbum(name, filterJSON string) (int64, error) {
	res, err := a.db.Exec(`INSERT INTO smart_albums(name, filter, created_at) VALUES(?, ?, ?)`,
		name, filterJSON, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (a *App) DeleteSmartAlbum(id int64) error {
	_, err := a.db.Exec(`DELETE FROM smart_albums WHERE id=?`, id)
	return err
}

type smartFilter struct {
	Favorite  bool   `json:"favorite"`
	MinRating int    `json:"minRating"`
	Type      string `json:"type"`
	Q         string `json:"q"`
	OnlyGPS   bool   `json:"onlyGPS"`
}

func (a *App) SmartAlbumItems(filterJSON string, offset, limit int) []Media {
	if limit <= 0 {
		limit = 200
	}
	var f smartFilter
	if err := jsonUnmarshal(filterJSON, &f); err != nil {
		return []Media{}
	}
	conds, args := buildSmartWhere(f)
	args = append(args, limit, offset)
	rows, err := a.db.Query(`SELECT `+mediaCols+` FROM media WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY taken_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

func (a *App) SmartAlbumCount(filterJSON string) int {
	var f smartFilter
	if err := jsonUnmarshal(filterJSON, &f); err != nil {
		return 0
	}
	conds, args := buildSmartWhere(f)
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM media WHERE `+strings.Join(conds, " AND "), args...).Scan(&n)
	return n
}

func buildSmartWhere(f smartFilter) ([]string, []interface{}) {
	conds := []string{"in_safe=0"}
	args := []interface{}{}
	if f.Favorite {
		conds = append(conds, "favorite=1")
	}
	if f.MinRating > 0 {
		conds = append(conds, "rating>=?")
		args = append(args, f.MinRating)
	}
	if f.Type != "" {
		conds = append(conds, "type=?")
		args = append(args, f.Type)
	}
	if f.OnlyGPS {
		conds = append(conds, "(lat!=0 OR lon!=0)")
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		lq := "%" + strings.ToLower(q) + "%"
		conds = append(conds, "(name_lower LIKE ? OR path_lower LIKE ? OR LOWER(camera) LIKE ?)")
		args = append(args, lq, lq, lq)
	}
	return conds, args
}

func jsonUnmarshal(s string, v interface{}) error { return json.Unmarshal([]byte(s), v) }

// ---------- сейф ----------

type SafeInfo struct {
	Exists   bool   `json:"exists"`
	Type     string `json:"type"`
	Hint     string `json:"hint"`
	Unlocked bool   `json:"unlocked"`
}

func (a *App) SafeInfo() SafeInfo {
	var info SafeInfo
	info.Unlocked = a.unlocked
	var t, h string
	if err := a.db.QueryRow(`SELECT pwd_type, hint FROM albums WHERE protected=1 LIMIT 1`).Scan(&t, &h); err == nil {
		info.Exists = true
		info.Type = t
		info.Hint = h
	}
	return info
}

func (a *App) CreateSafe(password, pwdType, hint string) error {
	log.Printf("[CreateSafe] type=%q pwdLen=%d", pwdType, len(password))
	if len(password) < 4 {
		return fmt.Errorf("пароль слишком короткий")
	}
	if pwdType != "pin" && pwdType != "text" && pwdType != "pattern" {
		return fmt.Errorf("неверный тип пароля: %q", pwdType)
	}
	var existing int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM albums WHERE protected=1`).Scan(&existing); err != nil {
		return fmt.Errorf("проверка: %w", err)
	}
	if existing > 0 {
		return fmt.Errorf("сейф уже создан")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	res, err := a.db.Exec(`INSERT INTO albums(name, created_at, protected, pwd_hash, pwd_type, hint)
		VALUES('Сейф', ?, 1, ?, ?, ?)`, time.Now().Unix(), string(h), pwdType, hint)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	id, _ := res.LastInsertId()
	log.Printf("[CreateSafe] created id=%d", id)
	a.unlocked = true
	return nil
}

func (a *App) UnlockSafe(password string) bool {
	var hash string
	if err := a.db.QueryRow(`SELECT pwd_hash FROM albums WHERE protected=1 LIMIT 1`).Scan(&hash); err != nil {
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil {
		a.unlocked = true
		return true
	}
	return false
}

func (a *App) LockSafe()            { a.unlocked = false }
func (a *App) IsSafeUnlocked() bool { return a.unlocked }

func (a *App) DeleteSafe() error {
	if !a.unlocked {
		return fmt.Errorf("сейф заблокирован")
	}
	var safeID int64
	if err := a.db.QueryRow(`SELECT id FROM albums WHERE protected=1 LIMIT 1`).Scan(&safeID); err != nil {
		return fmt.Errorf("сейф не найден")
	}
	_, _ = a.db.Exec(`UPDATE media SET in_safe=0 WHERE in_safe=1`)
	_, _ = a.db.Exec(`DELETE FROM album_items WHERE album_id=?`, safeID)
	_, _ = a.db.Exec(`DELETE FROM albums WHERE id=?`, safeID)
	a.unlocked = false
	log.Printf("[DeleteSafe] removed safe id=%d", safeID)
	return nil
}

func (a *App) SafeItems(offset, limit int) []Media {
	if !a.unlocked {
		return []Media{}
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.db.Query(`SELECT `+mediaCols+` FROM media
		WHERE in_safe=1 ORDER BY taken_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return []Media{}
	}
	defer rows.Close()
	return scanMedia(rows)
}

func (a *App) SafeCount() int {
	if !a.unlocked {
		return 0
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM media WHERE in_safe=1`).Scan(&n)
	return n
}

func (a *App) AddToSafe(hash string) error {
	if !a.unlocked {
		return fmt.Errorf("сейф заблокирован")
	}
	var safeID int64
	if err := a.db.QueryRow(`SELECT id FROM albums WHERE protected=1 LIMIT 1`).Scan(&safeID); err != nil {
		return fmt.Errorf("сейф не создан")
	}
	if _, err := a.db.Exec(`UPDATE media SET in_safe=1 WHERE hash=?`, hash); err != nil {
		return err
	}
	_, _ = a.db.Exec(`INSERT OR IGNORE INTO album_items(album_id, media_hash, added_at) VALUES(?, ?, ?)`,
		safeID, hash, time.Now().Unix())
	return nil
}

func (a *App) AddManyToSafe(hashes []string) error {
	for _, h := range hashes {
		_ = a.AddToSafe(h)
	}
	return nil
}

func (a *App) RemoveFromSafe(hash string) error {
	if !a.unlocked {
		return fmt.Errorf("сейф заблокирован")
	}
	if _, err := a.db.Exec(`UPDATE media SET in_safe=0 WHERE hash=?`, hash); err != nil {
		return err
	}
	var safeID int64
	if err := a.db.QueryRow(`SELECT id FROM albums WHERE protected=1 LIMIT 1`).Scan(&safeID); err == nil {
		_, _ = a.db.Exec(`DELETE FROM album_items WHERE album_id=? AND media_hash=?`, safeID, hash)
	}
	return nil
}

// ---------- удаление файлов ----------

func (a *App) DeleteFiles(hashes []string) error {
	var paths []string
	for _, h := range hashes {
		if m, ok := a.getByHash(h); ok {
			paths = append(paths, m.Path)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("нет файлов для удаления")
	}

	switch runtime.GOOS {
	case "windows":
		var b strings.Builder
		b.WriteString("Add-Type -AssemblyName Microsoft.VisualBasic\r\n")
		b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
		for _, p := range paths {
			b.WriteString("[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile(" +
				psQuote(p) + ", 'OnlyErrorDialogs', 'SendToRecycleBin')\r\n")
		}
		if err := runPowerShellFile(b.String()); err != nil {
			return err
		}
	case "darwin":
		var b strings.Builder
		b.WriteString("tell application \"Finder\"\n")
		for _, p := range paths {
			esc := strings.ReplaceAll(p, `"`, `\"`)
			b.WriteString("\tdelete POSIX file \"" + esc + "\"\n")
		}
		b.WriteString("end tell\n")
		cmd := hiddenCmd("osascript", "-e", b.String())
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("osascript: %v — %s", err, string(out))
		}
	default:
		args := append([]string{"trash"}, paths...)
		if _, err := exec.LookPath("gio"); err == nil {
			cmd := hiddenCmd("gio", args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("gio trash: %v — %s", err, string(out))
			}
		} else if _, err := exec.LookPath("trash-put"); err == nil {
			cmd := hiddenCmd("trash-put", paths...)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("trash-put: %v — %s", err, string(out))
			}
		} else {
			return fmt.Errorf("нет gio / trash-put для корзины")
		}
	}

	for _, h := range hashes {
		_, _ = a.db.Exec(`DELETE FROM album_items WHERE media_hash = ?`, h)
		_, _ = a.db.Exec(`DELETE FROM media WHERE hash = ?`, h)
		_ = os.Remove(filepath.Join(a.thumbDir, h+".jpg"))
	}
	return nil
}

// ---------- дубликаты ----------

type DuplicateGroup struct {
	ContentHash string  `json:"contentHash"`
	Count       int     `json:"count"`
	Items       []Media `json:"items"`
}

func (a *App) Duplicates() []DuplicateGroup {
	rows, err := a.db.Query(`SELECT content_hash, COUNT(*) c FROM media
		WHERE content_hash != '' AND in_safe=0
		GROUP BY content_hash HAVING c>1 ORDER BY c DESC LIMIT 500`)
	if err != nil {
		return []DuplicateGroup{}
	}
	defer rows.Close()
	out := make([]DuplicateGroup, 0)
	for rows.Next() {
		var ch string
		var cnt int
		if err := rows.Scan(&ch, &cnt); err != nil {
			continue
		}
		rr, err := a.db.Query(`SELECT `+mediaCols+` FROM media WHERE content_hash=? ORDER BY size DESC`, ch)
		if err != nil {
			continue
		}
		items := scanMedia(rr)
		rr.Close()
		out = append(out, DuplicateGroup{ContentHash: ch, Count: cnt, Items: items})
	}
	return out
}

// ---------- файловые операции ----------

func (a *App) RevealInFileManager(hash string) error {
	m, ok := a.getByHash(hash)
	if !ok {
		return fmt.Errorf("not found")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = hiddenCmd("explorer", "/select,"+m.Path)
	case "darwin":
		cmd = hiddenCmd("open", "-R", m.Path)
	default:
		cmd = hiddenCmd("xdg-open", filepath.Dir(m.Path))
	}
	return cmd.Start()
}

func (a *App) OpenFile(hash string) error {
	m, ok := a.getByHash(hash)
	if !ok {
		return fmt.Errorf("not found")
	}
	if m.InSafe && !a.unlocked {
		return fmt.Errorf("заблокировано")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = hiddenCmd("cmd", "/c", "start", "", m.Path)
	case "darwin":
		cmd = hiddenCmd("open", m.Path)
	default:
		cmd = hiddenCmd("xdg-open", m.Path)
	}
	return cmd.Start()
}

func (a *App) CopyPathsToClipboard(hashes []string) error {
	var lines []string
	for _, h := range hashes {
		if m, ok := a.getByHash(h); ok {
			lines = append(lines, m.Path)
		}
	}
	if len(lines) == 0 {
		return fmt.Errorf("пусто")
	}
	return writeClipboardText(strings.Join(lines, "\n"))
}

func (a *App) CopyFilesToClipboard(hashes []string) error {
	var paths []string
	for _, h := range hashes {
		if m, ok := a.getByHash(h); ok {
			paths = append(paths, m.Path)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("пусто")
	}
	switch runtime.GOOS {
	case "windows":
		var b strings.Builder
		b.WriteString("Add-Type -AssemblyName System.Windows.Forms\r\n")
		b.WriteString("$col = New-Object System.Collections.Specialized.StringCollection\r\n")
		for _, p := range paths {
			b.WriteString("$col.Add(" + psQuote(p) + ") | Out-Null\r\n")
		}
		b.WriteString("[System.Windows.Forms.Clipboard]::SetFileDropList($col)\r\n")
		return runPowerShellFile(b.String())
	case "darwin":
		var parts []string
		for _, p := range paths {
			parts = append(parts, `POSIX file "`+strings.ReplaceAll(p, `"`, `\"`)+`"`)
		}
		script := `set the clipboard to {` + strings.Join(parts, ", ") + `}`
		cmd := hiddenCmd("osascript", "-e", script)
		return cmd.Run()
	default:
		uri := strings.Join(paths, "\n")
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd := hiddenCmd("wl-copy")
			cmd.Stdin = strings.NewReader(uri)
			return cmd.Run()
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := hiddenCmd("xclip", "-selection", "clipboard")
			cmd.Stdin = strings.NewReader(uri)
			return cmd.Run()
		}
		return fmt.Errorf("нет xclip/wl-copy")
	}
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func writeClipboardText(text string) error {
	switch runtime.GOOS {
	case "windows":
		script := "Set-Clipboard -Value " + psQuote(text)
		return runPowerShellScript(script)
	case "darwin":
		cmd := hiddenCmd("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd := hiddenCmd("wl-copy")
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := hiddenCmd("xclip", "-selection", "clipboard")
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		}
		return fmt.Errorf("нет xclip/wl-copy")
	}
}

func (a *App) Stats() map[string]int {
	m := map[string]int{}
	rows, err := a.db.Query(`SELECT type, COUNT(*) FROM media WHERE in_safe=0 GROUP BY type`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err == nil {
			m[t] = n
		}
	}
	return m
}
