package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
)

type ChannelDef struct {
	Name          string   `yaml:"name" json:"name"`
	Group         string   `yaml:"group" json:"group"`
	TVGID         string   `yaml:"tvg_id" json:"tvg_id"`
	TVGName       string   `yaml:"tvg_name" json:"tvg_name"`
	Logo          string   `yaml:"logo" json:"logo"`
	URL           string   `yaml:"url" json:"url"`
	ChNo          *int     `yaml:"chno" json:"chno"`
	HTTPUserAgent string   `yaml:"http_user_agent" json:"http_user_agent,omitempty"`
	HTTPOrigin    string   `yaml:"http_origin" json:"http_origin,omitempty"`
	HTTPReferrer  string   `yaml:"http_referrer" json:"http_referrer,omitempty"`
	KodiProps     []string `yaml:"kodi_props" json:"kodi_props,omitempty"`
	Radio         bool     `yaml:"radio" json:"radio,omitempty"`
}

var groupBaseChNo = map[string]int{
	"PT Geral":                    1,
	"MZ Geral":                    20,
	"Notícias":                    100,
	"ES Geral & TDT":              200,
	"Galiza":                      230,
	"Regional & Local":            260,
	"Filmes & Docs":               300,
	"Sci-Fi & Cult Retro":         350,
	"Natureza & Slow TV":          400,
	"Desporto":                    500,
	"Infantil & Kids":             600,
	"Música & Video":              700,
	"Cívico & Parlamento":         800,
	"Religião":                    850,
	"Speedrunning & Marathons":    1000,
	"Mario & Romhacks":            1050,
	"Tetris":                      1100,
	"Indie & Variety Gaming":      1150,
	"Diag":                        2000,
	"PT Rádio":                    5000,
	"MZ Rádio":                    5100,
	"ES Rádio":                    6000,
	"NL Rádio":                    7000,
	"BE Rádio":                    7200,
	"Rádio Global":                8000,
	"Esperanto & Afrikaans Rádio": 8200,
}

var epgSourceHeaders = []string{
	"https://kiefte.eu/iptv/epg.xml.gz",
	"https://raw.githubusercontent.com/LaPingvino/iptv/main/dist/epg.xml.gz",
	"https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-m3upt.xml.xz",
	"https://raw.githubusercontent.com/Free-TV/IPTV/master/epg.xml.gz",
}

type UpstreamEPGFeed struct {
	Label string
	URL   string
	Comp  string // "gz" or "xz"
}

var upstreamFeeds = []UpstreamEPGFeed{
	{"M3UPT (PT)", "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-m3upt.xml.xz", "xz"},
	{"EPGShare Spain", "https://epgshare01.online/epgshare01/epg_ripper_ES1.xml.gz", "gz"},
	{"EPGShare Netherlands", "https://epgshare01.online/epgshare01/epg_ripper_NL1.xml.gz", "gz"},
}

func isRadio(ch ChannelDef) bool {
	g := strings.ToLower(ch.Group)
	return strings.Contains(g, "rádio") || strings.Contains(g, "radio") || ch.Radio
}

func RunBuildDist(dataDir, distDir string) error {
	log.Printf("[Builder] Reading channel configurations from %s...", dataDir)
	files, err := filepath.Glob(filepath.Join(dataDir, "*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no yaml channel files found in %s", dataDir)
	}
	sort.Strings(files)

	var channels []ChannelDef
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var list []ChannelDef
		if err := yaml.Unmarshal(b, &list); err == nil && len(list) > 0 {
			channels = append(channels, list...)
			continue
		}
		var wrapped struct {
			Channels []ChannelDef `yaml:"channels"`
		}
		if err := yaml.Unmarshal(b, &wrapped); err == nil && len(wrapped.Channels) > 0 {
			channels = append(channels, wrapped.Channels...)
		}
	}

	if len(channels) == 0 {
		return fmt.Errorf("zero channels parsed from data directory")
	}

	// Assign numbers
	used := make(map[int]bool)
	for _, ch := range channels {
		if ch.ChNo != nil {
			used[*ch.ChNo] = true
		}
	}

	alloc := make(map[string]int)
	for i := range channels {
		if channels[i].ChNo == nil {
			grp := channels[i].Group
			if grp == "" {
				grp = "Outros"
			}
			base := groupBaseChNo[grp]
			if base == 0 {
				base = 9000
			}
			cur := base
			if alloc[grp] > cur {
				cur = alloc[grp]
			}
			for used[cur] {
				cur++
			}
			channels[i].ChNo = &cur
			used[cur] = true
			alloc[grp] = cur + 1
		}
	}

	sort.Slice(channels, func(i, j int) bool {
		c1, c2 := 99999, 99999
		if channels[i].ChNo != nil {
			c1 = *channels[i].ChNo
		}
		if channels[j].ChNo != nil {
			c2 = *channels[j].ChNo
		}
		return c1 < c2
	})

	if err := os.MkdirAll(distDir, 0755); err != nil {
		return err
	}

	// 1. Build Playlists (playlist.m3u8, tv.m3u8, radio.m3u8)
	header := fmt.Sprintf("#EXTM3U url-tvg=\"%s\" x-tvg-url=\"%s\"\n\n",
		strings.Join(epgSourceHeaders, ","), strings.Join(epgSourceHeaders, ","))

	var masterLines []string
	var tvLines []string
	var radioLines []string

	masterLines = append(masterLines, header)
	tvLines = append(tvLines, header)
	radioLines = append(radioLines, header)

	tvCount := 0
	radioCount := 0

	for _, ch := range channels {
		entry := formatChannelM3U(ch)
		masterLines = append(masterLines, entry)
		if isRadio(ch) {
			radioLines = append(radioLines, entry)
			radioCount++
		} else {
			tvLines = append(tvLines, entry)
			tvCount++
		}
	}

	masterPath := filepath.Join(distDir, "playlist.m3u8")
	tvPath := filepath.Join(distDir, "tv.m3u8")
	radioPath := filepath.Join(distDir, "radio.m3u8")
	jsonPath := filepath.Join(distDir, "channels.json")

	_ = os.WriteFile(masterPath, []byte(strings.Join(masterLines, "\n\n")+"\n"), 0644)
	_ = os.WriteFile(tvPath, []byte(strings.Join(tvLines, "\n\n")+"\n"), 0644)
	_ = os.WriteFile(radioPath, []byte(strings.Join(radioLines, "\n\n")+"\n"), 0644)

	jsonBytes, _ := json.MarshalIndent(channels, "", "  ")
	_ = os.WriteFile(jsonPath, jsonBytes, 0644)

	log.Printf("[Builder] Playlists compiled: %d TV, %d Radio (Total: %d)", tvCount, radioCount, len(channels))

	// 2. Build Master EPG (epg.xml, epg.xml.gz)
	return buildMasterEPG(channels, distDir)
}

func formatChannelM3U(ch ChannelDef) string {
	name := ch.Name
	if name == "" {
		name = "Unknown"
	}
	tvgName := ch.TVGName
	if tvgName == "" {
		tvgName = name
	}
	group := ch.Group
	if group == "" {
		group = "Outros"
	}

	var inf []string
	inf = append(inf, "#EXTINF:-1")
	if ch.TVGID != "" {
		inf = append(inf, fmt.Sprintf("tvg-id=\"%s\"", ch.TVGID))
	}
	inf = append(inf, fmt.Sprintf("tvg-name=\"%s\"", tvgName))
	if ch.Logo != "" {
		inf = append(inf, fmt.Sprintf("tvg-logo=\"%s\"", ch.Logo))
	}
	inf = append(inf, fmt.Sprintf("group-title=\"%s\"", group))
	if ch.ChNo != nil {
		inf = append(inf, fmt.Sprintf("tvg-chno=\"%d\"", *ch.ChNo))
	}
	if isRadio(ch) {
		inf = append(inf, "radio=\"true\"")
	}

	line1 := strings.Join(inf, " ") + "," + name

	var lines []string
	lines = append(lines, line1)

	if ch.HTTPUserAgent != "" {
		ua := ch.HTTPUserAgent
		if !strings.HasPrefix(ua, "\"") {
			ua = "\"" + ua + "\""
		}
		lines = append(lines, "#EXTVLCOPT:http-user-agent="+ua)
	}
	if ch.HTTPOrigin != "" {
		lines = append(lines, "#EXTVLCOPT:http-origin="+ch.HTTPOrigin)
	}
	if ch.HTTPReferrer != "" {
		lines = append(lines, "#EXTVLCOPT:http-referrer="+ch.HTTPReferrer)
	}
	for _, prop := range ch.KodiProps {
		lines = append(lines, "#KODIPROP:"+prop)
	}

	lines = append(lines, ch.URL)
	return strings.Join(lines, "\n")
}

func buildMasterEPG(channels []ChannelDef, distDir string) error {
	log.Printf("[Builder] Concurrently fetching upstream EPGs and generating channel schedules...")

	targetIDs := make(map[string]bool)
	for _, ch := range channels {
		if ch.TVGID != "" {
			targetIDs[ch.TVGID] = true
			clean := strings.Split(ch.TVGID, "@")[0]
			targetIDs[clean] = true
		}
	}

	type epgResult struct {
		channels   map[string]string
		programmes []string
	}

	resChan := make(chan epgResult, len(upstreamFeeds))
	var wg sync.WaitGroup

	chRegex := regexp.MustCompile(`(?s)(<channel id="([^"]+)">.*?</channel>)`)
	progRegex := regexp.MustCompile(`(?s)(<programme [^>]*channel="([^"]+)"[^>]*>.*?</programme>)`)

	client := &http.Client{Timeout: 30 * time.Second}

	for _, feed := range upstreamFeeds {
		wg.Add(1)
		go func(f UpstreamEPGFeed) {
			defer wg.Done()
			req, err := http.NewRequest("GET", f.URL, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", "Mozilla/5.0")
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("[Builder] Warning: Failed to fetch %s: %v", f.Label, err)
				return
			}
			defer resp.Body.Close()

			var r io.Reader = resp.Body
			if f.Comp == "xz" {
				xzReader, err := xz.NewReader(resp.Body)
				if err != nil {
					log.Printf("[Builder] Warning: xz decompress error on %s: %v", f.Label, err)
					return
				}
				r = xzReader
			} else if f.Comp == "gz" {
				gzReader, err := gzip.NewReader(resp.Body)
				if err != nil {
					log.Printf("[Builder] Warning: gzip decompress error on %s: %v", f.Label, err)
					return
				}
				defer gzReader.Close()
				r = gzReader
			}

			data, err := io.ReadAll(r)
			if err != nil {
				return
			}

			xmlStr := string(data)
			extractedCh := make(map[string]string)
			var extractedProg []string

			chMatches := chRegex.FindAllStringSubmatch(xmlStr, -1)
			for _, m := range chMatches {
				fullTag, id := m[1], m[2]
				clean := strings.Split(id, "@")[0]
				if targetIDs[id] || targetIDs[clean] {
					extractedCh[id] = fullTag
				}
			}

			progMatches := progRegex.FindAllStringSubmatch(xmlStr, -1)
			for _, m := range progMatches {
				fullTag, id := m[1], m[2]
				clean := strings.Split(id, "@")[0]
				if targetIDs[id] || targetIDs[clean] {
					extractedProg = append(extractedProg, fullTag)
				}
			}

			log.Printf("[Builder] ✓ [%s] Extracted %d channels, %d programmes", f.Label, len(extractedCh), len(extractedProg))
			resChan <- epgResult{channels: extractedCh, programmes: extractedProg}
		}(feed)
	}

	wg.Wait()
	close(resChan)

	finalChannels := make(map[string]string)
	var finalProgrammes []string

	for r := range resChan {
		for id, tag := range r.channels {
			if _, exists := finalChannels[id]; !exists {
				finalChannels[id] = tag
			}
		}
		finalProgrammes = append(finalProgrammes, r.programmes...)
	}

	// Add Linear Stations using actual Go live schedule
	espStation := NewLinearStation(filepath.Join(ProjectDir, "pkg", "iptv-live-bridge", "esperantotv"), "esperanto", 10.0)
	espXML := epgManager.GetLinearEPG(espStation, "EsperantoTV.eo@SD", "Esperanto TV", "eo")
	_ = os.WriteFile(filepath.Join(distDir, "esperanto_epg.xml"), []byte(espXML), 0644)
	extractLocalEPG(espXML, "EsperantoTV.eo@SD", finalChannels, &finalProgrammes)

	bahaiStation := NewLinearStation(filepath.Join(ProjectDir, "pkg", "iptv-live-bridge", "bahaitv"), "bahai", 8.333333)
	bahaiXML := epgManager.GetLinearEPG(bahaiStation, "BahaiStudioSessions.tv@HD", "Bahá'í Studio Sessions TV", "en")
	extractLocalEPG(bahaiXML, "BahaiStudioSessions.tv@HD", finalChannels, &finalProgrammes)

	// Add Live Twitch EPG
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	twitchXML := epgManager.GetTwitchEPG(ctx)
	_ = os.WriteFile(filepath.Join(distDir, "twitch_epg.xml"), []byte(twitchXML), 0644)
	extractFullXMLTV(twitchXML, finalChannels, &finalProgrammes)

	// Assemble final XMLTV output
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE tv SYSTEM \"xmltv.dtd\">\n")
	sb.WriteString("<tv source-info-url=\"https://kiefte.eu/iptv\" generator-info-name=\"IPTV Master Go EPG Engine\">\n")

	for _, chTag := range finalChannels {
		sb.WriteString(chTag)
		sb.WriteString("\n")
	}
	for _, progTag := range finalProgrammes {
		sb.WriteString(progTag)
		sb.WriteString("\n")
	}
	sb.WriteString("</tv>\n")

	fullXML := sb.String()
	xmlPath := filepath.Join(distDir, "epg.xml")
	gzPath := filepath.Join(distDir, "epg.xml.gz")

	_ = os.WriteFile(xmlPath, []byte(fullXML), 0644)

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte(fullXML))
	_ = gw.Close()
	_ = os.WriteFile(gzPath, gzBuf.Bytes(), 0644)

	log.Printf("[Builder] Successfully generated %s (%d bytes, %d channels)", xmlPath, len(fullXML), len(finalChannels))
	log.Printf("[Builder] Successfully generated %s (%d bytes)", gzPath, gzBuf.Len())

	return nil
}

func extractLocalEPG(xmlStr, channelID string, channels map[string]string, programmes *[]string) {
	channels[channelID] = fmt.Sprintf("  <channel id=\"%s\"><display-name>%s</display-name></channel>", channelID, channelID)
	progRegex := regexp.MustCompile(`(?s)(<programme [^>]*channel="` + regexp.QuoteMeta(channelID) + `"[^>]*>.*?</programme>)`)
	matches := progRegex.FindAllString(xmlStr, -1)
	*programmes = append(*programmes, matches...)
}

func extractFullXMLTV(xmlStr string, channels map[string]string, programmes *[]string) {
	chRegex := regexp.MustCompile(`(?s)(<channel id="([^"]+)">.*?</channel>)`)
	progRegex := regexp.MustCompile(`(?s)(<programme [^>]*channel="([^"]+)"[^>]*>.*?</programme>)`)

	for _, m := range chRegex.FindAllStringSubmatch(xmlStr, -1) {
		channels[m[2]] = m[1]
	}
	for _, m := range progRegex.FindAllStringSubmatch(xmlStr, -1) {
		*programmes = append(*programmes, m[1])
	}
}
