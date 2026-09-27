package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type EPGChannelDef struct {
	ID             string
	Name           string
	IsGame         bool
	GameName       string
	Login          string
	IsFollowedRank bool
	Rank           int
	Group          string // playlist group, e.g. "Events & Marathons"
	StreamPath     string // URL path under /iptv/, e.g. "twitch/gamesdonequick"
}

// TwitchNowEntry is the per-channel "what is on right now" decision made while
// building the Twitch EPG, including which fallback is in effect. It is kept in
// memory and written to dist/twitch_now.json for the Now TV pages (channel 460).
type TwitchNowEntry struct {
	TvgID      string `json:"tvg_id"`
	Name       string `json:"name"`
	Group      string `json:"group"`
	StreamPath string `json:"stream_path"`
	// State: live, raid, host, relay (followed streamer in same game),
	// circle (mirror channel), offline, standby, unknown.
	State   string `json:"state"`
	Who     string `json:"who"` // who is actually on screen, if known
	Game    string `json:"game"`
	Title   string `json:"title"`
	Viewers int    `json:"viewers"`
	Note    string `json:"note,omitempty"` // fallback reason, e.g. "espelho", "raid"
}

type EPGManager struct {
	mu             sync.RWMutex
	twitchXML      string
	twitchTS       time.Time
	twitchUpdating bool
	unifiedXML     string
	unifiedTS      time.Time
	esperantoXML   string
	esperantoTS    time.Time
	bahaiXML       string
	bahaiTS        time.Time
	twitchNow      []TwitchNowEntry
}

var epgManager = &EPGManager{}

func init() {
	// Cold-start recovery from disk so the bridge never boots with an empty EPG
	diskPaths := []string{
		filepath.Join("/var/lib/iptv-live-bridge", "dist", "twitch_lapingvino_iptv_epg.xml"),
		filepath.Join("/home/joop/iptv", "dist", "twitch_lapingvino_iptv_epg.xml"),
		filepath.Join("/var/lib/iptv-live-bridge", "dist", "twitch_epg.xml"),
		filepath.Join("/home/joop/iptv", "dist", "twitch_epg.xml"),
	}
	for _, dp := range diskPaths {
		if b, err := os.ReadFile(dp); err == nil && len(b) > 0 {
			epgManager.twitchXML = string(b)
			epgManager.twitchTS = time.Now()
			return
		}
	}
	epgManager.twitchXML = fallbackBaselineEPG()
	epgManager.twitchTS = time.Now()
}

func getTwitchEPGChannels() []EPGChannelDef {
	// The service runs with ProtectHome=yes, so prefer the deployed data dir
	// (synced by sync.sh, matches the published playlist) over the project tree.
	var files []string
	for _, dir := range []string{
		filepath.Join(MediaDir, "data"),
		"/var/lib/iptv-live-bridge/data",
		"/usr/share/iptv-live-bridge/data",
		filepath.Join(ProjectDir, "data"),
	} {
		if f, err := filepath.Glob(filepath.Join(dir, "*.yaml")); err == nil && len(f) > 0 {
			files = f
			break
		}
	}
	if len(files) == 0 {
		return fallbackTwitchChannels()
	}

	var channels []EPGChannelDef
	seen := make(map[string]bool)

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var list []ChannelDef
		_ = yaml.Unmarshal(b, &list)
		for _, ch := range list {
			if strings.Contains(ch.URL, "twitch") && ch.TVGID != "" {
				if seen[ch.TVGID] {
					continue
				}
				seen[ch.TVGID] = true

				cleanURL := strings.Split(ch.URL, "?")[0]
				cleanURL = strings.TrimRight(cleanURL, "/")

				if strings.Contains(cleanURL, "/twitch/followed/") {
					parts := strings.Split(cleanURL, "/twitch/followed/")
					rank, _ := strconv.Atoi(parts[len(parts)-1])
					if rank < 1 {
						rank = 1
					}
					channels = append(channels, EPGChannelDef{
						ID:             ch.TVGID,
						Name:           ch.Name,
						IsFollowedRank: true,
						Rank:           rank,
						Group:          ch.Group,
						StreamPath:     iptvStreamPath(ch.URL),
					})
					continue
				}

				parts := strings.Split(cleanURL, "/")
				if len(parts) == 0 {
					continue
				}

				target, _ := url.PathUnescape(parts[len(parts)-1])
				isGame := strings.Contains(cleanURL, "/game/") || strings.Contains(cleanURL, "/group/")

				def := EPGChannelDef{
					ID:         ch.TVGID,
					Name:       ch.Name,
					Group:      ch.Group,
					StreamPath: iptvStreamPath(ch.URL),
				}
				if isGame {
					def.IsGame = true
					def.GameName = target
				} else {
					def.IsGame = false
					def.Login = strings.ToLower(target)
				}
				channels = append(channels, def)
			}
		}
	}

	if len(channels) == 0 {
		return fallbackTwitchChannels()
	}
	return channels
}

func fallbackTwitchChannels() []EPGChannelDef {
	return []EPGChannelDef{
		{ID: "Speedrun.tv", Login: "speedrun", Name: "Speedrun.com 24/7"},
		{ID: "GamesDoneQuick.tv", Login: "gamesdonequick", Name: "Games Done Quick"},
		{ID: "ESAMarathon.tv", Login: "esamarathon", Name: "European Speedrunner Assembly"},
		{ID: "TASVideos.tv", Login: "tasvideos", Name: "TASVideos"},
		{ID: "SmallAnt.tv", Login: "smallant", Name: "SmallAnt"},
		{ID: "Ryukahr.tv", Login: "ryukahr", Name: "Ryukahr"},
		{ID: "GrandPOOBear.tv", Login: "grandpoobear", Name: "GrandPOOBear"},
		{ID: "CarlSagan42.tv", Login: "carlsagan42", Name: "CarlSagan42"},
		{ID: "DGR.tv", Login: "dgr_dave", Name: "DGR"},
		{ID: "RBPimlico.tv", Login: "rbpimlico", Name: "RBPimlico"},
		{ID: "ClassicTetris.tv", Login: "classictetris", Name: "Classic Tetris World Championship"},
		{ID: "HardDrop.tv", Login: "harddrop", Name: "Hard Drop Tetris"},
		{ID: "BobRoss.tv", Login: "bobross", Name: "Bob Ross"},
		{ID: "LofiGirl.tv", Login: "lofigirl", Name: "Lofi Girl"},
		{ID: "NASALive.tv", Login: "nasa", Name: "NASA Live"},
	}
}

func (m *EPGManager) GetTwitchEPG(ctx context.Context) string {
	m.mu.RLock()
	cached := m.twitchXML
	isStale := time.Since(m.twitchTS) >= 2*time.Minute
	isUpdating := m.twitchUpdating
	m.mu.RUnlock()

	// If we have cached XML in memory, return it immediately (lazy, non-blocking!)
	if cached != "" {
		if isStale && !isUpdating {
			m.mu.Lock()
			if !m.twitchUpdating {
				m.twitchUpdating = true
				go func() {
					defer func() {
						m.mu.Lock()
						m.twitchUpdating = false
						m.mu.Unlock()
					}()
					bgCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
					defer cancel()
					xml, err := m.buildTwitchEPG(bgCtx)
					if err == nil && xml != "" {
						m.mu.Lock()
						m.twitchXML = xml
						m.twitchTS = time.Now()
						m.mu.Unlock()

						// Asynchronously save to files
						for _, name := range []string{"twitch_lapingvino_iptv_epg.xml", "twitch_epg.xml"} {
							p1 := filepath.Join(MediaDir, "dist", name)
							_ = os.MkdirAll(filepath.Dir(p1), 0755)
							_ = os.WriteFile(p1, []byte(xml), 0644)

							p2 := filepath.Join(ProjectDir, "dist", name)
							_ = os.MkdirAll(filepath.Dir(p2), 0755)
							_ = os.WriteFile(p2, []byte(xml), 0644)
						}
					}
				}()
			}
			m.mu.Unlock()
		}
		return cached
	}

	// Cold start (no cache yet): try loading from disk first for instant response
	diskPaths := []string{
		filepath.Join(MediaDir, "dist", "twitch_lapingvino_iptv_epg.xml"),
		filepath.Join(ProjectDir, "dist", "twitch_lapingvino_iptv_epg.xml"),
		filepath.Join(MediaDir, "dist", "twitch_epg.xml"),
		filepath.Join(ProjectDir, "dist", "twitch_epg.xml"),
	}
	for _, dp := range diskPaths {
		if b, err := os.ReadFile(dp); err == nil && len(b) > 0 {
			m.mu.Lock()
			m.twitchXML = string(b)
			m.twitchTS = time.Now()
			m.mu.Unlock()
			log.Printf("[Twitch EPG] Loaded instant cache from %s", dp)
			return string(b)
		}
	}

	xml, err := m.buildTwitchEPG(ctx)
	if err != nil {
		log.Printf("[Twitch EPG] Warning: Failed to build Twitch EPG: %v", err)
		return fallbackBaselineEPG()
	}

	m.mu.Lock()
	m.twitchXML = xml
	m.twitchTS = time.Now()
	m.mu.Unlock()

	go func() {
		for _, name := range []string{"twitch_lapingvino_iptv_epg.xml", "twitch_epg.xml"} {
			p1 := filepath.Join(MediaDir, "dist", name)
			_ = os.MkdirAll(filepath.Dir(p1), 0755)
			_ = os.WriteFile(p1, []byte(xml), 0644)

			p2 := filepath.Join(ProjectDir, "dist", name)
			_ = os.MkdirAll(filepath.Dir(p2), 0755)
			_ = os.WriteFile(p2, []byte(xml), 0644)
		}
	}()

	return xml
}

func sanitizeAlias(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func (m *EPGManager) buildTwitchEPG(ctx context.Context) (string, error) {
	channels := getTwitchEPGChannels()

	// Batch 1: every user channel, every mirror-circle member, every game.
	var queries []string
	seenUser := map[string]bool{}
	addUser := func(login string) {
		login = strings.ToLower(login)
		if login == "" || seenUser[login] {
			return
		}
		seenUser[login] = true
		queries = append(queries, gqlUserQuery("u_"+sanitizeAlias(login), login))
	}
	seenGame := map[string]bool{}
	addGame := func(game string) {
		k := strings.ToLower(game)
		if game == "" || seenGame[k] {
			return
		}
		seenGame[k] = true
		queries = append(queries, gqlGameQuery("g_"+sanitizeAlias(game), game))
	}
	for _, ch := range channels {
		if ch.IsFollowedRank {
			continue
		}
		if ch.IsGame {
			if games, ok := GameGroups[strings.ToLower(ch.GameName)]; ok {
				for _, g := range games {
					addGame(g)
				}
			} else {
				addGame(ch.GameName)
			}
			for _, fb := range gameCircleMembers(ch) {
				addUser(fb)
			}
		} else {
			addUser(ch.Login)
			for _, fb := range creatorCircles[ch.Login] {
				addUser(fb)
			}
		}
	}
	data, err := twitchGQL(ctx, queries)
	if err != nil {
		return "", err
	}
	log.Printf("[Twitch EPG] GQL response keys in data: %d", len(data))

	users := map[string]*gqlUser{}
	for login := range seenUser {
		if raw, ok := data["u_"+sanitizeAlias(login)]; ok {
			var u gqlUser
			if json.Unmarshal(raw, &u) == nil && u.Login != "" {
				users[login] = &u
			}
		}
	}
	games := map[string]*gqlGame{}
	for k := range seenGame {
		if raw, ok := data["g_"+sanitizeAlias(k)]; ok {
			var g gqlGame
			if json.Unmarshal(raw, &g) == nil {
				games[k] = &g
			}
		}
	}

	// Batch 2: top stream in the last played game of offline channels (Resolve step 7).
	var lastQueries []string
	lastSeen := map[string]bool{}
	for _, u := range users {
		if u.Stream != nil {
			continue
		}
		if lg := u.lastGame(); lg != "" && !lastSeen[strings.ToLower(lg)] && games[strings.ToLower(lg)] == nil {
			lastSeen[strings.ToLower(lg)] = true
			lastQueries = append(lastQueries, gqlGameQuery("l_"+sanitizeAlias(lg), lg))
		}
	}
	gameTop := map[string]*gqlGameStream{}
	for k, g := range games {
		gameTop[k] = pickGameStream(g)
	}
	if lastData, err := twitchGQL(ctx, lastQueries); err == nil {
		for k := range lastSeen {
			var g gqlGame
			if raw, ok := lastData["l_"+sanitizeAlias(k)]; ok && json.Unmarshal(raw, &g) == nil {
				gameTop[k] = pickGameStream(&g)
			}
		}
	}

	now := time.Now().UTC()
	prevStart := now.Add(-3 * time.Hour).Format("20060102150405 +0000")
	curStart := now.Add(-1 * time.Hour).Format("20060102150405 +0000")
	curStop := now.Add(2 * time.Hour).Format("20060102150405 +0000")
	nextStop := now.Add(5 * time.Hour).Format("20060102150405 +0000")

	liveFollows := twitchMgr.GetRankedLiveFollows(ctx)
	var nowEntries []TwitchNowEntry

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE tv SYSTEM \"xmltv.dtd\">\n")
	sb.WriteString("<tv source-info-url=\"https://kiefte.eu/iptv\" generator-info-name=\"LaPingvino Twitch IPTV Live EPG Engine\">\n")

	for _, ch := range channels {
		sb.WriteString(fmt.Sprintf("  <channel id=\"%s\"><display-name>%s</display-name></channel>\n",
			html.EscapeString(ch.ID), html.EscapeString(ch.Name)))

		var title string
		var desc string
		var category string = "Gaming"
		cur := TwitchNowEntry{TvgID: ch.ID, Name: ch.Name, Group: ch.Group, StreamPath: ch.StreamPath, State: "unknown"}

		if ch.IsFollowedRank {
			idx := ch.Rank - 1
			if idx < len(liveFollows) {
				s := liveFollows[idx]
				title = fmt.Sprintf("#%d: %s - %s", ch.Rank, s.DisplayName, s.Title)
				desc = fmt.Sprintf("Followed streamer #%d (%s) playing %s with %d viewers.", ch.Rank, s.DisplayName, s.Game, s.Viewers)
				category = s.Game
				cur.State, cur.Who, cur.Game, cur.Title, cur.Viewers = "live", s.DisplayName, s.Game, s.Title, s.Viewers
			} else {
				cur.State = "standby"
				title = fmt.Sprintf("Followed Streamer #%d (Standby)", ch.Rank)
				desc = fmt.Sprintf("Slot reserved for live followed streamer #%d. Standby active.", ch.Rank)
				category = "Standby"
			}
		} else {
			var d nowDecision
			if ch.IsGame {
				category = ch.GameName
				if gl, ok := GameGroups[strings.ToLower(ch.GameName)]; ok {
					d = nowDecision{State: "offline", Game: ch.GameName}
					for _, g := range gl {
						if dd := decideGame(EPGChannelDef{GameName: g, StreamPath: ch.StreamPath}, games[strings.ToLower(g)], users); dd.State == "live" {
							d = dd
							break
						}
					}
					if d.State != "live" {
						d = decideGame(ch, nil, users)
					}
				} else {
					d = decideGame(ch, games[strings.ToLower(ch.GameName)], users)
				}
			} else {
				d = decideUser(ch.Login, users, gameTop, liveFollows)
			}
			name := users[ch.Login].name(ch.Name)
			if ch.IsGame {
				name = ch.GameName
			}
			switch d.State {
			case "live":
				category = d.Game
				title = fmt.Sprintf("%s - %s", d.Game, d.Title)
				desc = fmt.Sprintf("Live on %s streaming %s with %d viewers.", d.Who, d.Game, d.Viewers)
			case "offline":
				title = fmt.Sprintf("%s (Offline)", name)
				desc = fmt.Sprintf("%s is offline. Standby slate active.", name)
				if d.Game != "" && !ch.IsGame {
					desc = fmt.Sprintf("%s is offline. Last broadcast was %s.", name, d.Game)
				}
			default:
				if d.Game != "" {
					category = d.Game
				}
				title = fmt.Sprintf("[%s → %s] %s", d.Note, d.Who, d.Title)
				desc = fmt.Sprintf("%s is offline. Relaying %s (%s) playing %s with %d viewers.", name, d.Who, d.Note, d.Game, d.Viewers)
			}
			cur.State, cur.Who, cur.Game, cur.Title, cur.Viewers = d.State, d.Who, d.Game, d.Title, d.Viewers
			cur.Note = d.Note
		}

		nowEntries = append(nowEntries, cur)

		// 1. Previous block (Past 3 hours)
		sb.WriteString(fmt.Sprintf("  <programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n",
			prevStart, curStart, html.EscapeString(ch.ID)))
		sb.WriteString(fmt.Sprintf("    <title lang=\"en\">%s (Previous)</title>\n", html.EscapeString(title)))
		sb.WriteString(fmt.Sprintf("    <desc lang=\"en\">%s</desc>\n", html.EscapeString(desc)))
		sb.WriteString(fmt.Sprintf("    <category lang=\"en\">%s</category>\n", html.EscapeString(category)))
		sb.WriteString("  </programme>\n")

		// 2. Current live block (Active now)
		sb.WriteString(fmt.Sprintf("  <programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n",
			curStart, curStop, html.EscapeString(ch.ID)))
		sb.WriteString(fmt.Sprintf("    <title lang=\"en\">%s</title>\n", html.EscapeString(title)))
		sb.WriteString(fmt.Sprintf("    <desc lang=\"en\">%s</desc>\n", html.EscapeString(desc)))
		sb.WriteString(fmt.Sprintf("    <category lang=\"en\">%s</category>\n", html.EscapeString(category)))
		sb.WriteString("  </programme>\n")

		// 3. Upcoming block (Next 3 hours)
		sb.WriteString(fmt.Sprintf("  <programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n",
			curStop, nextStop, html.EscapeString(ch.ID)))
		sb.WriteString(fmt.Sprintf("    <title lang=\"en\">%s (Upcoming)</title>\n", html.EscapeString(title)))
		sb.WriteString(fmt.Sprintf("    <desc lang=\"en\">%s</desc>\n", html.EscapeString(desc)))
		sb.WriteString(fmt.Sprintf("    <category lang=\"en\">%s</category>\n", html.EscapeString(category)))
		sb.WriteString("  </programme>\n")
	}

	sb.WriteString("</tv>\n")

	m.mu.Lock()
	m.twitchNow = nowEntries
	m.mu.Unlock()
	if b, err := json.MarshalIndent(nowEntries, "", " "); err == nil {
		p := filepath.Join(MediaDir, "dist", "twitch_now.json")
		if os.MkdirAll(filepath.Dir(p), 0755) == nil {
			_ = os.WriteFile(p, b, 0644)
		}
	}
	return sb.String(), nil
}

// TwitchNow returns the latest per-channel decisions from the Twitch EPG build,
// falling back to dist/twitch_now.json, and building synchronously if neither exists.
func (m *EPGManager) TwitchNow(ctx context.Context) []TwitchNowEntry {
	m.GetTwitchEPG(ctx) // triggers the usual lazy 2-minute background refresh
	m.mu.RLock()
	entries := m.twitchNow
	m.mu.RUnlock()
	if len(entries) > 0 {
		return entries
	}
	if b, err := os.ReadFile(filepath.Join(MediaDir, "dist", "twitch_now.json")); err == nil {
		if json.Unmarshal(b, &entries) == nil && len(entries) > 0 {
			return entries
		}
	}
	bctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if xml, err := m.buildTwitchEPG(bctx); err == nil && xml != "" {
		m.mu.Lock()
		m.twitchXML, m.twitchTS = xml, time.Now()
		entries = m.twitchNow
		m.mu.Unlock()
	}
	return entries
}

// gameCircleMembers mirrors ResolveGame's circle step for a game or group
// channel: bias-specific circle ("nes-tetris") first, then the game's own.
func gameCircleMembers(ch EPGChannelDef) []string {
	game := strings.ToLower(strings.ReplaceAll(ch.GameName, "-", " "))
	bias := ""
	if u, err := url.Parse("/" + ch.StreamPath); err == nil {
		bias = u.Query().Get("bias")
	}
	var out []string
	if bias != "" {
		out = append(out, creatorCircles[bias+"-"+game]...)
	}
	out = append(out, creatorCircles[game]...)
	out = append(out, creatorCircles[strings.ToLower(ch.GameName)]...)
	return out
}

// iptvStreamPath returns the part of a bridge URL after "/iptv/" (query kept).
func iptvStreamPath(u string) string {
	if i := strings.Index(u, "/iptv/"); i >= 0 {
		return u[i+len("/iptv/"):]
	}
	return u
}

func (m *EPGManager) GetLinearEPG(station *LinearStation, chID, chName, lang string) string {
	station.mu.RLock()
	schedule := station.schedule
	segDuration := station.segDuration
	station.mu.RUnlock()

	if len(schedule) == 0 {
		return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<tv><channel id=\"%s\"><display-name>%s</display-name></channel></tv>\n",
			html.EscapeString(chID), html.EscapeString(chName))
	}

	type Block struct {
		title    string
		desc     string
		category string
		duration float64
	}

	var blocks []Block
	var currentBlock *Block

	for _, seg := range schedule {
		if currentBlock == nil || seg.IsTransition {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &Block{
				title:    seg.Title,
				desc:     seg.Desc,
				category: seg.Category,
				duration: segDuration,
			}
		} else {
			currentBlock.duration += segDuration
		}
	}
	if currentBlock != nil {
		blocks = append(blocks, *currentBlock)
	}

	totalLoopDur := 0.0
	for _, b := range blocks {
		totalLoopDur += b.duration
	}

	now := float64(time.Now().Unix())
	startWindow := now - 86400
	endWindow := now + 86400*3

	offset := float64(int64(startWindow*1000)%(int64(totalLoopDur*1000))) / 1000.0
	curPos := 0.0
	curIdx := 0
	progStart := startWindow

	for i, b := range blocks {
		if curPos+b.duration > offset {
			curIdx = i
			progStart = startWindow - (offset - curPos)
			break
		}
		curPos += b.duration
	}

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE tv SYSTEM \"xmltv.dtd\">\n")
	sb.WriteString("<tv source-info-url=\"https://kiefte.eu/iptv\" generator-info-name=\"IPTV Linear 24/7 EPG Engine\">\n")
	sb.WriteString(fmt.Sprintf("  <channel id=\"%s\"><display-name>%s</display-name></channel>\n",
		html.EscapeString(chID), html.EscapeString(chName)))

	currTime := progStart
	for currTime < endWindow {
		prog := blocks[curIdx]
		pStart := time.Unix(int64(currTime), 0).UTC().Format("20060102150405 +0000")
		pStop := time.Unix(int64(currTime+prog.duration), 0).UTC().Format("20060102150405 +0000")

		sb.WriteString(fmt.Sprintf("  <programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n",
			pStart, pStop, html.EscapeString(chID)))
		sb.WriteString(fmt.Sprintf("    <title lang=\"%s\">%s</title>\n", lang, html.EscapeString(prog.title)))
		sb.WriteString(fmt.Sprintf("    <desc lang=\"%s\">%s</desc>\n", lang, html.EscapeString(prog.desc)))
		sb.WriteString(fmt.Sprintf("    <category lang=\"%s\">%s</category>\n", lang, html.EscapeString(prog.category)))
		sb.WriteString("  </programme>\n")

		currTime += prog.duration
		curIdx = (curIdx + 1) % len(blocks)
	}

	sb.WriteString("</tv>\n")
	return sb.String()
}

func fallbackBaselineEPG() string {
	channels := getTwitchEPGChannels()
	now := time.Now().UTC()
	curStart := now.Add(-1 * time.Hour).Format("20060102150405 +0000")
	curStop := now.Add(3 * time.Hour).Format("20060102150405 +0000")

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE tv SYSTEM \"xmltv.dtd\">\n")
	sb.WriteString("<tv source-info-url=\"https://kiefte.eu/iptv\" generator-info-name=\"LaPingvino Twitch IPTV Live EPG Engine\">\n")

	for _, ch := range channels {
		sb.WriteString(fmt.Sprintf("  <channel id=\"%s\"><display-name>%s</display-name></channel>\n",
			html.EscapeString(ch.ID), html.EscapeString(ch.Name)))

		var title string
		var desc string
		var cat string = "Gaming"

		if ch.IsFollowedRank {
			title = fmt.Sprintf("Followed Streamer #%d", ch.Rank)
			desc = fmt.Sprintf("Followed streamer slot #%d.", ch.Rank)
			cat = "Gaming"
		} else if ch.IsGame {
			title = fmt.Sprintf("%s - No username cached", ch.GameName)
			desc = fmt.Sprintf("No streamer is currently cached for category %s. Stream relay is standing by.", ch.GameName)
			cat = ch.GameName
		} else {
			name := ch.Name
			if name == "" {
				name = ch.Login
			}
			title = fmt.Sprintf("%s - Live status unknown", name)
			desc = fmt.Sprintf("Live status for %s is currently unknown. Stream relay is standing by.", name)
		}

		sb.WriteString(fmt.Sprintf("  <programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n",
			curStart, curStop, html.EscapeString(ch.ID)))
		sb.WriteString(fmt.Sprintf("    <title lang=\"en\">%s</title>\n", html.EscapeString(title)))
		sb.WriteString(fmt.Sprintf("    <desc lang=\"en\">%s</desc>\n", html.EscapeString(desc)))
		sb.WriteString(fmt.Sprintf("    <category lang=\"en\">%s</category>\n", html.EscapeString(cat)))
		sb.WriteString("  </programme>\n")
	}

	sb.WriteString("</tv>\n")
	return sb.String()
}
