package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type EPGChannelDef struct {
	ID       string
	Name     string
	IsGame   bool
	GameName string
	Login    string
}

type EPGManager struct {
	mu           sync.RWMutex
	twitchXML    string
	twitchTS     time.Time
	unifiedXML   string
	unifiedTS    time.Time
	esperantoXML string
	esperantoTS  time.Time
	bahaiXML     string
	bahaiTS      time.Time
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
			break
		}
	}
}

func getTwitchEPGChannels() []EPGChannelDef {
	dataDir := filepath.Join(ProjectDir, "data")
	files, err := filepath.Glob(filepath.Join(dataDir, "*.yaml"))
	if err != nil || len(files) == 0 {
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
				parts := strings.Split(cleanURL, "/")
				if len(parts) == 0 {
					continue
				}

				target, _ := url.PathUnescape(parts[len(parts)-1])
				isGame := strings.Contains(cleanURL, "/game/")

				def := EPGChannelDef{
					ID:   ch.TVGID,
					Name: ch.Name,
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
	if m.twitchXML != "" && time.Since(m.twitchTS) < 90*time.Second {
		xml := m.twitchXML
		m.mu.RUnlock()
		return xml
	}
	m.mu.RUnlock()

	xml, err := m.buildTwitchEPG(ctx)
	if err != nil {
		log.Printf("[Twitch EPG] Warning: Failed to refresh Twitch EPG: %v", err)
		m.mu.RLock()
		if m.twitchXML != "" {
			cached := m.twitchXML
			m.mu.RUnlock()
			log.Printf("[Twitch EPG] Retaining existing in-memory EPG cache")
			return cached
		}
		m.mu.RUnlock()

		// Try loading from disk
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
				log.Printf("[Twitch EPG] Loaded fallback from %s", dp)
				return string(b)
			}
		}
		return fallbackEmptyEPG()
	}

	m.mu.Lock()
	m.twitchXML = xml
	m.twitchTS = time.Now()
	m.mu.Unlock()

	// Asynchronously save to both files for user convenience
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

	var queries []string
	for _, ch := range channels {
		if ch.IsGame {
			alias := "g_" + sanitizeAlias(ch.GameName)
			queries = append(queries, fmt.Sprintf(`
			%s: game(name: "%s") {
				name
				streams(first: 1) {
					edges {
						node {
							broadcaster { login displayName }
							title
							viewersCount
						}
					}
				}
			}`, alias, html.EscapeString(ch.GameName)))
		} else {
			alias := "u_" + sanitizeAlias(ch.Login)
			queries = append(queries, fmt.Sprintf(`
			%s: user(login: "%s") {
				displayName
				stream {
					title
					viewersCount
					game { name }
				}
				raid {
					targetChannel { login displayName }
				}
				hosting {
					login
					stream { viewersCount }
				}
				primaryTeam {
					displayName
					members {
						edges {
							node {
								login
								displayName
								stream { viewersCount title game { name } }
							}
						}
					}
				}
				lastBroadcast {
					title
					game { name }
				}
			}`, alias, ch.Login))
		}
	}

	fullQuery := "query BatchTwitchEPG {\n" + strings.Join(queries, "\n") + "\n}"
	payload, _ := json.Marshal(map[string]string{"query": fullQuery})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gql.twitch.tv/gql", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Client-Id", "kimne78kx3ncx6brgo4mv6wki5h1ko")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("twitch GQL returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data map[string]json.RawMessage `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	now := time.Now().UTC()
	prevStart := now.Add(-3 * time.Hour).Format("20060102150405 +0000")
	curStart := now.Add(-1 * time.Hour).Format("20060102150405 +0000")
	curStop := now.Add(2 * time.Hour).Format("20060102150405 +0000")
	nextStop := now.Add(5 * time.Hour).Format("20060102150405 +0000")

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

		if ch.IsGame {
			alias := "g_" + sanitizeAlias(ch.GameName)
			raw, exists := result.Data[alias]
			if exists {
				var gData struct {
					Name    string `json:"name"`
					Streams struct {
						Edges []struct {
							Node struct {
								Broadcaster struct {
									Login       string `json:"login"`
									DisplayName string `json:"displayName"`
								} `json:"broadcaster"`
								Title        string `json:"title"`
								ViewersCount int    `json:"viewersCount"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"streams"`
				}
				_ = json.Unmarshal(raw, &gData)
				category = ch.GameName
				if len(gData.Streams.Edges) > 0 {
					top := gData.Streams.Edges[0].Node
					title = fmt.Sprintf("%s - %s", ch.GameName, top.Title)
					desc = fmt.Sprintf("Live on %s streaming %s with %d viewers.", top.Broadcaster.DisplayName, ch.GameName, top.ViewersCount)
				} else {
					title = fmt.Sprintf("%s (Standby)", ch.GameName)
					desc = fmt.Sprintf("No active broadcast in category %s right now. Standby active.", ch.GameName)
				}
			} else {
				title = fmt.Sprintf("%s (Standby)", ch.GameName)
				desc = fmt.Sprintf("Category %s live stream relay.", ch.GameName)
			}
		} else {
			alias := "u_" + sanitizeAlias(ch.Login)
			raw, exists := result.Data[alias]
			if exists {
				var uData struct {
					DisplayName string `json:"displayName"`
					Stream      *struct {
						Title        string `json:"title"`
						ViewersCount int    `json:"viewersCount"`
						Game         *struct {
							Name string `json:"name"`
						} `json:"game"`
					} `json:"stream"`
					Raid *struct {
						TargetChannel *struct {
							Login       string `json:"login"`
							DisplayName string `json:"displayName"`
						} `json:"targetChannel"`
					} `json:"raid"`
					Hosting *struct {
						Login  string `json:"login"`
						Stream *struct {
							ViewersCount int `json:"viewersCount"`
						} `json:"stream"`
					} `json:"hosting"`
					PrimaryTeam *struct {
						DisplayName string `json:"displayName"`
						Members     struct {
							Edges []struct {
								Node struct {
									Login       string `json:"login"`
									DisplayName string `json:"displayName"`
									Stream      *struct {
										ViewersCount int    `json:"viewersCount"`
										Title        string `json:"title"`
										Game         *struct {
											Name string `json:"name"`
										} `json:"game"`
									} `json:"stream"`
								} `json:"node"`
							} `json:"edges"`
						} `json:"members"`
					} `json:"primaryTeam"`
					LastBroadcast *struct {
						Title string `json:"title"`
						Game  *struct {
							Name string `json:"name"`
						} `json:"game"`
					} `json:"lastBroadcast"`
				}
				_ = json.Unmarshal(raw, &uData)

				name := uData.DisplayName
				if name == "" {
					name = ch.Name
				}

				if uData.Stream != nil {
					// Live stream
					gName := "Gaming"
					if uData.Stream.Game != nil && uData.Stream.Game.Name != "" {
						gName = uData.Stream.Game.Name
					}
					category = gName
					if uData.Stream.Title != "" {
						title = fmt.Sprintf("%s - %s", gName, uData.Stream.Title)
					} else {
						title = fmt.Sprintf("%s Live", name)
					}
					desc = fmt.Sprintf("Live on %s streaming %s with %d viewers.", name, gName, uData.Stream.ViewersCount)
				} else if uData.Raid != nil && uData.Raid.TargetChannel != nil {
					target := uData.Raid.TargetChannel.DisplayName
					title = fmt.Sprintf("[Raid -> %s] Stream ended", target)
					desc = fmt.Sprintf("%s raided %s. Stream auto-relaying to %s.", name, target, target)
				} else if uData.Hosting != nil && uData.Hosting.Stream != nil {
					target := uData.Hosting.Login
					title = fmt.Sprintf("[Hosting %s] Host Relay", target)
					desc = fmt.Sprintf("%s is currently hosting %s with %d viewers.", name, target, uData.Hosting.Stream.ViewersCount)
				} else if uData.PrimaryTeam != nil {
					var bestTeammate string
					var bestViewers int
					var bestGame string
					for _, e := range uData.PrimaryTeam.Members.Edges {
						if e.Node.Stream != nil && e.Node.Stream.ViewersCount > bestViewers && strings.ToLower(e.Node.Login) != ch.Login {
							bestTeammate = e.Node.DisplayName
							bestViewers = e.Node.Stream.ViewersCount
							if e.Node.Stream.Game != nil {
								bestGame = e.Node.Stream.Game.Name
							}
						}
					}
					if bestTeammate != "" {
						title = fmt.Sprintf("[Relay: %s] %s", bestTeammate, bestGame)
						desc = fmt.Sprintf("%s is offline. Auto-relaying %s teammate %s (%d viewers).", name, uData.PrimaryTeam.DisplayName, bestTeammate, bestViewers)
						if bestGame != "" {
							category = bestGame
						}
					}
				}

				if title == "" {
					// Check creator circles fallback
					if circle, ok := creatorCircles[ch.Login]; ok && len(circle) > 0 {
						title = fmt.Sprintf("[Circle: %s] Community Relay", circle[0])
						desc = fmt.Sprintf("%s is offline. Priority relay to community circle member %s.", name, circle[0])
					} else {
						title = fmt.Sprintf("%s (Offline)", name)
						if uData.LastBroadcast != nil && uData.LastBroadcast.Game != nil {
							desc = fmt.Sprintf("%s is offline. Last broadcast was %s.", name, uData.LastBroadcast.Game.Name)
						} else {
							desc = fmt.Sprintf("%s is offline. Standby slate active.", name)
						}
					}
				}
			} else {
				title = fmt.Sprintf("%s (Offline)", ch.Name)
				desc = fmt.Sprintf("%s is offline. Standby slate active.", ch.Name)
			}
		}

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
	return sb.String(), nil
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

func fallbackEmptyEPG() string {
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<tv><channel id=\"Speedrun.tv\"><display-name>Speedrun.com 24/7</display-name></channel></tv>\n"
}
