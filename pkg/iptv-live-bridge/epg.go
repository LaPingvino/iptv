package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type EPGChannelDef struct {
	ID    string
	Login string
	Name  string
}

var twitchEPGList = []EPGChannelDef{
	{"Speedrun.tv", "speedrun", "Speedrun.com 24/7"},
	{"GamesDoneQuick.tv", "gamesdonequick", "Games Done Quick"},
	{"ESAMarathon.tv", "esamarathon", "European Speedrunner Assembly"},
	{"TASVideos.tv", "tasvideos", "TASVideos"},
	{"MitchFlowerPower.tv", "mitchflowerpower", "MitchFlowerPower"},
	{"SmallAnt.tv", "smallant", "SmallAnt"},
	{"GrandPOOBear.tv", "grandpoobear", "GrandPOOBear"},
	{"SimpleFlips.tv", "simpleflips", "SimpleFlips"},
	{"Ryukahr.tv", "ryukahr", "Ryukahr"},
	{"PangaeaPanga.tv", "pangaeapanga", "PangaeaPanga"},
	{"CarlSagan42.tv", "carlsagan42", "CarlSagan42"},
	{"Aurateur.tv", "aurateur", "Aurateur"},
	{"HardDrop.tv", "harddrop", "Hard Drop Tetris"},
	{"ClassicTetris.tv", "classictetris", "Classic Tetris World Championship"},
	{"DGR.tv", "dgr_dave", "DGR"},
	{"BobRoss.tv", "bobross", "Bob Ross"},
	{"MST3K.tv", "mst3k", "Mystery Science Theater 3000"},
	{"ShoutFactoryTV.tv", "shoutfactorytv", "Shout! Factory TV"},
	{"LofiGirl.tv", "lofigirl", "Lofi Girl"},
	{"Monstercat.tv", "monstercat", "Monstercat"},
	{"NASALive.tv", "nasa", "NASA Live"},
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

func (m *EPGManager) GetTwitchEPG(ctx context.Context) string {
	m.mu.RLock()
	if m.twitchXML != "" && time.Since(m.twitchTS) < 90*time.Second {
		xml := m.twitchXML
		m.mu.RUnlock()
		return xml
	}
	m.mu.RUnlock()

	xml := m.buildTwitchEPG(ctx)
	m.mu.Lock()
	m.twitchXML = xml
	m.twitchTS = time.Now()
	m.mu.Unlock()

	// Asynchronously save to dist dir
	go func() {
		p := filepath.Join(MediaDir, "dist", "twitch_epg.xml")
		os.MkdirAll(filepath.Dir(p), 0755)
		_ = os.WriteFile(p, []byte(xml), 0644)
	}()

	return xml
}

func (m *EPGManager) buildTwitchEPG(ctx context.Context) string {
	var queries []string
	for _, ch := range twitchEPGList {
		alias := "u_" + strings.ReplaceAll(strings.ToLower(ch.Login), "-", "_")
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

	fullQuery := "query BatchTwitchEPG {\n" + strings.Join(queries, "\n") + "\n}"
	payload, _ := json.Marshal(map[string]string{"query": fullQuery})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gql.twitch.tv/gql", bytes.NewReader(payload))
	if err != nil {
		return fallbackEmptyEPG()
	}
	req.Header.Set("Client-Id", "kimne78kx3ncx6brgo4mv6wki5h1ko")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallbackEmptyEPG()
	}
	defer resp.Body.Close()

	var result struct {
		Data map[string]*struct {
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
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fallbackEmptyEPG()
	}

	now := time.Now().UTC()
	startStr := now.Add(-1 * time.Hour).Format("20060102150405 +0000")
	stopStr := now.Add(3 * time.Hour).Format("20060102150405 +0000")

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE tv SYSTEM \"xmltv.dtd\">\n")
	sb.WriteString("<tv source-info-url=\"https://kiefte.eu/iptv\" generator-info-name=\"IPTV Live Twitch Real-Time EPG Engine\">\n")

	for _, ch := range twitchEPGList {
		sb.WriteString(fmt.Sprintf("  <channel id=\"%s\"><display-name>%s</display-name></channel>\n",
			html.EscapeString(ch.ID), html.EscapeString(ch.Name)))

		alias := "u_" + strings.ReplaceAll(strings.ToLower(ch.Login), "-", "_")
		u := result.Data[alias]

		title := ch.Name
		desc := "Live Twitch broadcast"
		category := "Gaming"

		if u != nil && u.Stream != nil {
			// Live primary stream
			if u.Stream.Title != "" {
				title = u.Stream.Title
			}
			gameName := "Gaming"
			if u.Stream.Game != nil && u.Stream.Game.Name != "" {
				gameName = u.Stream.Game.Name
			}
			category = gameName
			desc = fmt.Sprintf("Live on %s playing %s with %d viewers", u.DisplayName, gameName, u.Stream.ViewersCount)
		} else if u != nil {
			// Streamer is offline: Check raid, host, or teammate fallback
			if u.Raid != nil && u.Raid.TargetChannel != nil {
				target := u.Raid.TargetChannel.DisplayName
				title = fmt.Sprintf("[Raid -> %s] Stream ended", target)
				desc = fmt.Sprintf("%s has raided %s. Stream auto-relaying to %s.", ch.Name, target, target)
			} else if u.Hosting != nil && u.Hosting.Stream != nil {
				target := u.Hosting.Login
				title = fmt.Sprintf("[Hosting %s] Live Host Relay", target)
				desc = fmt.Sprintf("%s is currently hosting %s with %d viewers.", ch.Name, target, u.Hosting.Stream.ViewersCount)
			} else if u.PrimaryTeam != nil {
				var bestTeammate string
				var bestViewers int
				var bestGame string
				for _, e := range u.PrimaryTeam.Members.Edges {
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
					desc = fmt.Sprintf("%s is offline. Auto-relaying %s teammate %s (%d viewers).", ch.Name, u.PrimaryTeam.DisplayName, bestTeammate, bestViewers)
					category = bestGame
				} else {
					title = fmt.Sprintf("%s (Offline)", ch.Name)
					if u.LastBroadcast != nil && u.LastBroadcast.Game != nil {
						desc = fmt.Sprintf("%s is currently offline. Last broadcast was %s.", ch.Name, u.LastBroadcast.Game.Name)
					} else {
						desc = fmt.Sprintf("%s is currently offline. Standby slate active.", ch.Name)
					}
				}
			} else {
				title = fmt.Sprintf("%s (Offline)", ch.Name)
				desc = fmt.Sprintf("%s is currently offline.", ch.Name)
			}
		}

		sb.WriteString(fmt.Sprintf("  <programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n",
			startStr, stopStr, html.EscapeString(ch.ID)))
		sb.WriteString(fmt.Sprintf("    <title lang=\"en\">%s</title>\n", html.EscapeString(title)))
		sb.WriteString(fmt.Sprintf("    <desc lang=\"en\">%s</desc>\n", html.EscapeString(desc)))
		sb.WriteString(fmt.Sprintf("    <category lang=\"en\">%s</category>\n", html.EscapeString(category)))
		sb.WriteString("  </programme>\n")
	}

	sb.WriteString("</tv>\n")
	return sb.String()
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
		duration float64
	}

	var blocks []Block
	var currentPrefix string
	var currentCount int

	for _, seg := range schedule {
		idx := strings.LastIndex(seg, "_")
		prefix := seg
		if idx != -1 {
			prefix = seg[:idx]
		}
		if prefix == currentPrefix {
			currentCount++
		} else {
			if currentCount > 0 {
				blocks = append(blocks, makeBlock(currentPrefix, float64(currentCount)*segDuration))
			}
			currentPrefix = prefix
			currentCount = 1
		}
	}
	if currentCount > 0 {
		blocks = append(blocks, makeBlock(currentPrefix, float64(currentCount)*segDuration))
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
		sb.WriteString(fmt.Sprintf("    <category lang=\"%s\">General</category>\n", lang))
		sb.WriteString("  </programme>\n")

		currTime += prog.duration
		curIdx = (curIdx + 1) % len(blocks)
	}

	sb.WriteString("</tv>\n")
	return sb.String()
}

func makeBlock(prefix string, dur float64) struct{ title, desc string; duration float64 } {
	title := strings.ReplaceAll(prefix, "_", " ")
	title = strings.Title(title)
	desc := fmt.Sprintf("Broadcast of %s", title)

	if strings.HasPrefix(prefix, "dok_estas_parto_01") {
		title = "Esperanto Estas: Enkonduko"
		desc = "Oficiala stacia vineto kaj enkonduko al la internacia lingvo Esperanto."
	} else if strings.HasPrefix(prefix, "mazi") {
		title = "Mazi en Gondolando"
		desc = "La legenda animacia kurso de Esperanto kun Mazi, Silvia kaj Bob."
	} else if strings.HasPrefix(prefix, "senlime") {
		title = "Senlime: Esperanto-Kurso"
		desc = "Moderna televida lingvokurso de Esperanto."
	} else if strings.HasPrefix(prefix, "dok_kef2005") {
		title = "KEF 2005: La Plejpleja Festivalo"
		desc = "Kultura Esperanto-Festivalo en Helsinki."
	}

	return struct{ title, desc string; duration float64 }{
		title:    title,
		desc:     desc,
		duration: dur,
	}
}

func fallbackEmptyEPG() string {
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<tv><channel id=\"Speedrun.tv\"><display-name>Speedrun.com 24/7</display-name></channel></tv>\n"
}
