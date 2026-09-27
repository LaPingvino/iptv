package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NowTV renders the Now Playing overview, a clock and the local weather as a
// live MPEG-TS channel, using a FanoutEngine (one on-demand ffmpeg shared by
// all viewers, stopped 30s after the last one leaves).
//
// The picture is composed from a handful of text files that ffmpeg's drawtext
// re-reads every frame. A Go goroutine rewrites them (clock every second,
// weather and channel list every 30s) using atomic renames.
type NowTVEngine struct {
	mu      sync.Mutex
	workDir string

	weatherLat, weatherLon float64
	weatherName            string

	engine *FanoutEngine

	pagesMu sync.Mutex
	pages   []nowPage
	pageIdx int

	pip    *PiP
	runCtx context.Context
}

// nowPage is one Teletext-style page of the dashboard.
type nowPage struct {
	title string
	body  string // bright lines (channel + status)
	dim   string // dim lines (stream title), interleaved with body via blank lines

	pipPath  string // stream shown in the corner insert (first live/fallback on the page)
	pipLabel string
}

var nowTV = newNowTV()

func newNowTV() *NowTVEngine {
	e := &NowTVEngine{weatherLat: 38.7223, weatherLon: -9.1393, weatherName: "Lisboa"}
	e.engine = &FanoutEngine{Name: "NowTV", OnStart: e.onStart, BuildCmd: e.buildCmd}
	return e
}

// ConfigureWeather sets the location used for the weather panel.
func (e *NowTVEngine) ConfigureWeather(lat, lon float64, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.weatherLat, e.weatherLon, e.weatherName = lat, lon, name
}

func (e *NowTVEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.engine.ServeHTTP(w, r) }

func (e *NowTVEngine) weather() (float64, float64, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.weatherLat, e.weatherLon, e.weatherName
}

// Text slots drawn on the card; each maps to one file in workDir.
var nowTVSlots = []string{"clock", "date", "pagehdr", "weather", "list", "listdim", "piplabel", "footer"}

func (e *NowTVEngine) slotPath(name string) string {
	return filepath.Join(e.workDir, name+".txt")
}

// writeAtomic writes a drawtext text file via rename. ffmpeg 9's drawtext
// renders nothing at all when the text starts with a newline or ends in an
// empty line, so empty lines become a single space and trailing newlines go.
func writeAtomic(path, content string) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = " "
		}
	}
	content = strings.Join(lines, "\n")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return
	}
	os.Rename(tmp, path)
}

// fontFile returns the first existing font path, or "" to let fontconfig pick.
func fontFile(candidates ...string) string {
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func (e *NowTVEngine) drawtext(slot, font string, size int, x, y, color string) string {
	f := "font=Sans"
	if font != "" {
		f = "fontfile=" + font
	}
	return fmt.Sprintf("drawtext=%s:textfile=%s:reload=1:expansion=none:fontsize=%d:fontcolor=%s:x=%s:y=%s:line_spacing=10",
		f, e.slotPath(slot), size, color, x, y)
}

// onStart seeds the text files ffmpeg reads and starts the refresher.
func (e *NowTVEngine) onStart(ctx context.Context) {
	if e.workDir == "" {
		dir, err := os.MkdirTemp("", "iptv-nowtv-")
		if err != nil {
			log.Printf("[NowTV] Cannot create work dir: %v", err)
			dir = os.TempDir()
		}
		e.workDir = dir
	}
	lat, lon, place := e.weather()
	// drawtext fails on missing files, so every slot must exist before ffmpeg starts.
	for _, s := range nowTVSlots {
		writeAtomic(e.slotPath(s), " ")
	}
	e.runCtx = ctx
	e.pip = &PiP{}
	if err := e.pip.Open(ctx); err != nil {
		log.Printf("[NowTV] PiP disabled: %v", err)
		e.pip = nil
	}
	e.writeClock()
	e.setPages([]nowPage{{title: "Now Playing", body: "A carregar…"}})

	// Data refresher (network): pages, weather and footer now and every 60s.
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			e.refreshData(ctx, lat, lon, place)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	// Clock every second, next page every 10s.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n++
				e.writeClock()
				if n%10 == 0 {
					e.nextPage()
				}
			}
		}
	}()
}

func (e *NowTVEngine) buildCmd(ctx context.Context) *exec.Cmd {
	sans := fontFile("/usr/share/fonts/TTF/DejaVuSans.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf")
	bold := fontFile("/usr/share/fonts/TTF/DejaVuSans-Bold.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf")
	mono := fontFile("/usr/share/fonts/TTF/DejaVuSansMono.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf")

	const pipX, pipY = 813, 440
	card := strings.Join([]string{
		// Header band, weather panel and insert frame
		"drawbox=x=0:y=0:w=iw:h=150:color=0x1e293b@1:t=fill",
		"drawbox=x=0:y=150:w=iw:h=4:color=0x3b82f6@1:t=fill",
		"drawbox=x=770:y=180:w=470:h=230:color=0x1e293b@1:t=fill",
		"drawbox=x=770:y=180:w=6:h=230:color=0xf59e0b@1:t=fill",
		fmt.Sprintf("drawbox=x=%d:y=%d:w=%d:h=%d:color=0x334155@1:t=fill", pipX-3, pipY-3, pipW+6, pipH+6),
		// Static title (drawtext text= needs escaping; keep it plain)
		fmt.Sprintf("drawtext=fontfile=%s:text='LaPingvino IPTV':fontsize=46:fontcolor=white:x=40:y=34", bold),
		e.drawtext("pagehdr", sans, 26, "42", "94", "0x93c5fd"),
		e.drawtext("clock", bold, 72, "w-tw-40", "22", "white"),
		e.drawtext("date", sans, 24, "w-tw-42", "108", "0xcbd5e1"),
		e.drawtext("weather", sans, 23, "796", "202", "white"),
		e.drawtext("list", mono, 22, "40", "185", "0xe2e8f0"),
		e.drawtext("listdim", mono, 22, "40", "185", "0x94a3b8"),
		e.drawtext("piplabel", sans, 18, strconv.Itoa(pipX), strconv.Itoa(pipY-26), "0xfbbf24"),
		e.drawtext("footer", sans, 20, "40", "680", "0x64748b"),
	}, ",")

	args := []string{"-nostdin", "-v", "warning",
		"-re", "-f", "lavfi", "-i", "color=c=0x0f172a:s=1280x720:r=25"}
	var audioMap string
	if e.pip != nil {
		args = append(args,
			"-thread_queue_size", "64", "-f", "rawvideo", "-pix_fmt", "yuv420p",
			"-video_size", fmt.Sprintf("%dx%d", pipW, pipH), "-framerate", strconv.Itoa(pipFPS), "-i", "pipe:3",
			"-thread_queue_size", "64", "-f", "s16le", "-ar", strconv.Itoa(pipRate), "-ac", "2", "-i", "pipe:4",
			"-filter_complex", fmt.Sprintf("[0:v]%s[bg];[bg][1:v]overlay=%d:%d:eof_action=repeat[v]", card, pipX, pipY),
			"-map", "[v]")
		audioMap = "2:a"
	} else {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
			"-filter_complex", "[0:v]"+card+"[v]", "-map", "[v]")
		audioMap = "1:a"
	}
	args = append(args, "-map", audioMap,
		"-c:v", "libx264", "-preset", "veryfast",
		"-pix_fmt", "yuv420p", "-g", "50", "-b:v", "1500k", "-maxrate", "2000k", "-bufsize", "4000k",
		"-c:a", "aac", "-b:a", "96k",
		"-mpegts_flags", "resend_headers+initial_discontinuity",
		"-f", "mpegts", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if e.pip != nil {
		cmd.ExtraFiles = e.pip.ReaderFiles()
	}
	return cmd
}

var ptWeekdays = []string{"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado"}
var ptMonths = []string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

func (e *NowTVEngine) writeClock() {
	now := time.Now()
	writeAtomic(e.slotPath("clock"), now.Format("15:04:05"))
	writeAtomic(e.slotPath("date"), fmt.Sprintf("%s, %d de %s", ptWeekdays[now.Weekday()], now.Day(), ptMonths[now.Month()-1]))
}

func (e *NowTVEngine) refreshData(ctx context.Context, lat, lon float64, place string) {
	writeAtomic(e.slotPath("weather"), weatherText(ctx, lat, lon, place))
	e.setPages(buildNowPages(ctx))
	writeAtomic(e.slotPath("footer"), "kiefte.eu/iptv/now  •  atualizado "+time.Now().Format("15:04"))
}

func (e *NowTVEngine) setPages(p []nowPage) {
	e.pagesMu.Lock()
	e.pages = p
	if e.pageIdx >= len(p) {
		e.pageIdx = 0
	}
	e.pagesMu.Unlock()
	e.writePage()
}

func (e *NowTVEngine) nextPage() {
	e.pagesMu.Lock()
	if len(e.pages) > 0 {
		e.pageIdx = (e.pageIdx + 1) % len(e.pages)
	}
	e.pagesMu.Unlock()
	e.writePage()
}

func (e *NowTVEngine) writePage() {
	e.pagesMu.Lock()
	defer e.pagesMu.Unlock()
	if len(e.pages) == 0 {
		return
	}
	pg := e.pages[e.pageIdx]
	writeAtomic(e.slotPath("pagehdr"), fmt.Sprintf("P460 · %d/%d · %s", e.pageIdx+1, len(e.pages), pg.title))
	writeAtomic(e.slotPath("list"), pg.body)
	writeAtomic(e.slotPath("listdim"), pg.dim)
	writeAtomic(e.slotPath("piplabel"), pg.pipLabel)
	if e.pip != nil && e.runCtx != nil {
		next := e.pages[(e.pageIdx+1)%len(e.pages)].pipPath
		e.pip.Show(e.runCtx, pg.pipPath, next)
	}
}

// buildNowPages turns the Twitch EPG decisions (dist/twitch_now.json) into
// pages: favourites, events, games and streamers, 7 channels per page, each
// with a status line and the title of what is actually on.
func buildNowPages(ctx context.Context) []nowPage {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	byGroup := make(map[string][]TwitchNowEntry)
	for _, en := range epgManager.TwitchNow(cctx) {
		byGroup[en.Group] = append(byGroup[en.Group], en)
	}
	sections := []struct{ group, title string }{
		{"LaPingvino Favorites", "Favoritos"},
		{"Events & Marathons", "Eventos & Maratonas"},
		{"Games (Top Live)", "Jogos · quem joga agora"},
		{"Streamers", "Streamers"},
	}
	const perPage = 7
	var pages []nowPage
	for _, sec := range sections {
		entries := byGroup[sec.group]
		if len(entries) == 0 {
			continue
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return playlistChNo(entries[i].StreamPath) < playlistChNo(entries[j].StreamPath)
		})
		nPages := (len(entries) + perPage - 1) / perPage
		for p := 0; p < nPages; p++ {
			end := (p + 1) * perPage
			if end > len(entries) {
				end = len(entries)
			}
			var bright, dim strings.Builder
			var pipPath, pipLabel string
			for _, en := range entries[p*perPage : end] {
				if pipPath == "" && en.State != "offline" && en.State != "standby" && en.State != "unknown" && en.State != "" {
					pipPath = en.StreamPath
					pipLabel = fmt.Sprintf("▶ %d  %s", playlistChNo(en.StreamPath), stripEmoji(en.Who))
				}
				b, d := nowRow(en, sec.group == "Games (Top Live)")
				bright.WriteString(b + "\n\n")
				dim.WriteString("\n" + d + "\n")
			}
			title := sec.title
			if nPages > 1 {
				title = fmt.Sprintf("%s (%d/%d)", sec.title, p+1, nPages)
			}
			pages = append(pages, nowPage{title: title, body: bright.String(), dim: dim.String(), pipPath: pipPath, pipLabel: pipLabel})
		}
	}
	if len(pages) == 0 {
		pages = []nowPage{{title: "Now Playing", body: nowListText(ctx)}}
	}
	return pages
}

// stripEmoji drops characters the dashboard's monospace font has no glyph for
// (emoji and pictographs), which would otherwise render as boxes.
func stripEmoji(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || (r >= 0xFE00 && r <= 0xFE0F) || r == 0x200D {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func shortViewers(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// nowRow renders a channel as a status line (~54 columns) and a title line.
func nowRow(en TwitchNowEntry, isGame bool) (string, string) {
	name := en.Name
	if i := strings.Index(name, " ("); i > 0 {
		name = name[:i]
	}
	if strings.HasPrefix(en.StreamPath, "twitch/followed/") && en.Who != "" {
		name = en.Who // favourite slots are named after whoever holds the rank
	}
	var mark, detail string
	title := en.Title
	switch en.State {
	case "live":
		mark = "●"
		if isGame {
			detail = en.Who + " · " + shortViewers(en.Viewers)
		} else {
			detail = en.Game + " · " + shortViewers(en.Viewers)
		}
	case "offline":
		mark, detail = "○", "offline"
		if en.Game != "" && !isGame {
			detail = "offline · último: " + en.Game
		}
	case "standby":
		mark, detail = "○", "em espera"
	case "unknown", "":
		mark, detail = "?", "estado desconhecido"
	default: // a fallback is on air
		mark, detail = "→", en.Who
		if en.Game != "" {
			detail += " · " + en.Game
		}
		if en.Note != "" {
			title = "(" + en.Note + ") " + title
		}
	}
	name, detail, title = stripEmoji(name), stripEmoji(detail), stripEmoji(title)
	line1 := fmt.Sprintf("%3d %s %-15s %s", playlistChNo(en.StreamPath), mark, truncRunes(name, 15), truncRunes(detail, 32))
	line2 := ""
	if strings.TrimSpace(title) != "" {
		line2 = "      " + truncRunes(strings.TrimSpace(title), 48)
	}
	return line1, line2
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// nowListText lists live channels from the Now Playing data, busiest first.
func nowListText(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var live []ChannelNow
	for _, c := range buildNowChannels(cctx) {
		if c.Live {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return "Nenhum stream ao vivo neste momento."
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].ChNo < live[j].ChNo })
	var b strings.Builder
	for i, c := range live {
		if i >= 12 {
			fmt.Fprintf(&b, "   … e mais %d", len(live)-i)
			break
		}
		who := c.DisplayName
		if who == "" {
			who = c.Name
		}
		game := c.Game
		if game == "" {
			game = "—"
		}
		fmt.Fprintf(&b, "%3d  %-17s %-22s %6d\n", c.ChNo, truncRunes(who, 17), truncRunes(game, 22), c.Viewers)
	}
	return b.String()
}

// Weather via Open-Meteo (no API key), cached for 10 minutes.
var weatherCache struct {
	sync.Mutex
	text string
	at   time.Time
	key  string
}

var wmoPT = map[int]string{
	0: "Céu limpo", 1: "Pouco nublado", 2: "Parcialmente nublado", 3: "Nublado",
	45: "Nevoeiro", 48: "Nevoeiro gelado",
	51: "Chuvisco fraco", 53: "Chuvisco", 55: "Chuvisco forte",
	61: "Chuva fraca", 63: "Chuva", 65: "Chuva forte",
	66: "Chuva gelada", 67: "Chuva gelada forte",
	71: "Neve fraca", 73: "Neve", 75: "Neve forte", 77: "Grãos de neve",
	80: "Aguaceiros fracos", 81: "Aguaceiros", 82: "Aguaceiros fortes",
	85: "Aguaceiros de neve", 86: "Aguaceiros de neve fortes",
	95: "Trovoada", 96: "Trovoada com granizo", 99: "Trovoada forte com granizo",
}

func weatherText(ctx context.Context, lat, lon float64, place string) string {
	key := fmt.Sprintf("%.4f,%.4f", lat, lon)
	weatherCache.Lock()
	if weatherCache.key == key && time.Since(weatherCache.at) < 10*time.Minute && weatherCache.text != "" {
		t := weatherCache.text
		weatherCache.Unlock()
		return t
	}
	weatherCache.Unlock()

	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lon))
	q.Set("current", "temperature_2m,apparent_temperature,weather_code,wind_speed_10m,relative_humidity_2m")
	q.Set("daily", "temperature_2m_max,temperature_2m_min,weather_code")
	q.Set("forecast_days", "3")
	q.Set("timezone", "auto")

	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, "https://api.open-meteo.com/v1/forecast?"+q.Encode(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return place + "\n\nMeteorologia indisponível"
	}
	defer resp.Body.Close()

	var w struct {
		Current struct {
			Temp     float64 `json:"temperature_2m"`
			Feels    float64 `json:"apparent_temperature"`
			Code     int     `json:"weather_code"`
			Wind     float64 `json:"wind_speed_10m"`
			Humidity float64 `json:"relative_humidity_2m"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
			Code []int     `json:"weather_code"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return place + "\n\nMeteorologia indisponível"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", place)
	fmt.Fprintf(&b, "%.0f°C  %s\n", w.Current.Temp, wmoPT[w.Current.Code])
	fmt.Fprintf(&b, "Sensação %.0f°  •  Vento %.0f km/h\n", w.Current.Feels, w.Current.Wind)
	for i := 1; i < len(w.Daily.Time) && i < 3; i++ {
		d, err := time.Parse("2006-01-02", w.Daily.Time[i])
		label := w.Daily.Time[i]
		if err == nil {
			label = strings.SplitN(ptWeekdays[d.Weekday()], "-", 2)[0]
		}
		fmt.Fprintf(&b, "%s  %.0f°/%.0f°  %s\n", label, w.Daily.Max[i], w.Daily.Min[i], wmoPT[w.Daily.Code[i]])
	}
	t := b.String()

	weatherCache.Lock()
	weatherCache.text, weatherCache.at, weatherCache.key = t, time.Now(), key
	weatherCache.Unlock()
	return t
}
