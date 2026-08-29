package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	Port        = 7555
	MediaDir    = "/var/lib/iptv-live-bridge"
	FallbackDir = "/usr/share/iptv-live-bridge"
	ProjectDir  = "/home/joop/iptv"
)

func getMediaDir(sub string) string {
	p1 := filepath.Join(MediaDir, sub)
	if _, err := os.Stat(p1); err == nil {
		return p1
	}
	p2 := filepath.Join(FallbackDir, sub)
	if _, err := os.Stat(p2); err == nil {
		return p2
	}
	return filepath.Join(ProjectDir, "pkg/iptv-live-bridge", sub)
}

func main() {
	if pEnv := os.Getenv("PORT"); pEnv != "" {
		if p, err := strconv.Atoi(pEnv); err == nil {
			Port = p
		}
	}

	buildDist := flag.Bool("build-dist", false, "Compile playlists and master EPG files from data/ into dist/ then exit")
	flag.IntVar(&Port, "port", Port, "HTTP listen port")
	flag.StringVar(&MediaDir, "media-dir", MediaDir, "Media root directory")
	flag.Parse()

	if *buildDist {
		dataDir := filepath.Join(ProjectDir, "data")
		distDir := filepath.Join(ProjectDir, "dist")
		if flag.NArg() > 0 {
			dataDir = flag.Arg(0)
		}
		if flag.NArg() > 1 {
			distDir = flag.Arg(1)
		}
		if err := RunBuildDist(dataDir, distDir); err != nil {
			log.Fatalf("[Builder] Fatal error: %v", err)
		}
		log.Printf("[Builder] All distribution files successfully generated in %s", distDir)
		os.Exit(0)
	}

	bvnEngine.SetPort(Port)

	esperantoDir := getMediaDir("esperantotv")
	bahaiDir := getMediaDir("bahaitv")

	esperantoStation := NewLinearStation(esperantoDir, "esperanto", 10.0)
	bahaiStation := NewLinearStation(bahaiDir, "bahai", 8.333333)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Normalize path: strip leading /iptv/ or /
		path := strings.Trim(r.URL.Path, "/")
		if strings.HasPrefix(path, "iptv/") {
			path = strings.Trim(strings.TrimPrefix(path, "iptv/"), "/")
		}

		params := r.URL.Query()
		bias := params.Get("bias")

		// 2. Health & Status
		if path == "" || path == "health" || path == "status" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"service":   "iptv-live-bridge",
				"version":   "4.0.0",
				"runtime":   "go",
				"timestamp": time.Now().Format(time.RFC3339),
			})
			return
		}

		// 2b. Real-Time EPG Handlers
		if path == "twitch/epg" || path == "twitch/epg.xml" || path == "epg/twitch.xml" || path == "twitch_lapingvino_iptv_epg.xml" || path == "dist/twitch_lapingvino_iptv_epg.xml" {
			xml := epgManager.GetTwitchEPG(r.Context())
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			w.Write([]byte(xml))
			return
		}

		if path == "esperanto/epg" || path == "esperanto/epg.xml" || path == "epg/esperanto.xml" {
			xml := epgManager.GetLinearEPG(esperantoStation, "EsperantoTV.eo@SD", "Esperanto TV", "eo")
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "max-age=300, must-revalidate")
			w.Write([]byte(xml))
			return
		}

		if path == "bahai/epg" || path == "bahai/epg.xml" || path == "epg/bahai.xml" {
			xml := epgManager.GetLinearEPG(bahaiStation, "BahaiStudioSessions.tv@HD", "Bahá'í Studio Sessions TV", "en")
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "max-age=300, must-revalidate")
			w.Write([]byte(xml))
			return
		}

		// 3. Static distribution files (playlist.m3u8, epg.xml.gz, epg.xml, all.m3u8)
		distFile := path
		if strings.HasPrefix(distFile, "dist/") {
			distFile = strings.TrimPrefix(distFile, "dist/")
		}
		distPaths := []string{
			filepath.Join(MediaDir, "dist", distFile),
			filepath.Join(FallbackDir, "dist", distFile),
			filepath.Join(ProjectDir, "dist", distFile),
		}
		for _, dp := range distPaths {
			if fi, err := os.Stat(dp); err == nil && !fi.IsDir() {
				serveDistFile(w, r, dp, distFile)
				return
			}
		}

		// 4. BVN Internal MPD (used by local ffmpeg)
		if path == "bvn_internal.mpd" {
			data, err := getBVNDynamicMPD(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/dash+xml")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			w.Write(data)
			return
		}

		// 5. BVN Live Decrypted Stream (/bvn, /bvn.ts, /nl/bvn, /nl/bvn.ts)
		if path == "bvn" || path == "bvn.ts" || path == "nl/bvn" || path == "nl/bvn.ts" {
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Connection", "close")

			if r.Method == http.MethodHead {
				return
			}

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			ch := bvnEngine.Subscribe()
			defer bvnEngine.Unsubscribe(ch)

			for {
				select {
				case <-r.Context().Done():
					return
				case chunk, ok := <-ch:
					if !ok {
						return
					}
					if _, err := w.Write(chunk); err != nil {
						return
					}
					flusher.Flush()
				}
			}
		}

		// 6. Linear Stations
		if path == "esperanto" || path == "esperantotv" || path == "esperanto/tv" || path == "esperantotv/tv" || path == "esperanto.m3u8" || path == "esperantotv.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Write([]byte(esperantoStation.Playlist("/iptv/testcard/esperanto_standby0.ts")))
			return
		}

		if path == "bahai" || path == "bahaitv" || path == "bahai/tv" || path == "bahaitv/tv" || path == "bahai.m3u8" || path == "bahaitv.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Write([]byte(bahaiStation.Playlist("/iptv/testcard/bahai_standby0.ts")))
			return
		}

		// 7. Disney Channel Portugal Fast Proxy
		if path == "disney" || path == "disney.m3u8" || path == "disney/playlist.m3u8" {
			upstreamURL := "http://151.80.18.177:86/Disney_Channel_HD/tracks-v1a1/mono.m3u8"
			m3u8, err := FetchAndMakeAbsoluteM3U8(r.Context(), upstreamURL)
			if err != nil {
				serveOfflineSlate(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Write([]byte(m3u8))
			return
		}

		// 8. Media Segments
		if strings.HasPrefix(path, "esperanto/") {
			seg := strings.TrimPrefix(path, "esperanto/")
			serveMediaFile(w, r, filepath.Join(esperantoDir, seg))
			return
		}
		if strings.HasPrefix(path, "bahai/") {
			seg := strings.TrimPrefix(path, "bahai/")
			serveMediaFile(w, r, filepath.Join(bahaiDir, seg))
			return
		}
		if strings.HasPrefix(path, "offline/") {
			seg := strings.TrimPrefix(path, "offline/")
			serveMediaFile(w, r, filepath.Join(getMediaDir("offline"), seg))
			return
		}
		if strings.HasPrefix(path, "testcard/") || strings.HasPrefix(path, "test/") {
			seg := strings.TrimPrefix(path, "testcard/")
			seg = strings.TrimPrefix(seg, "test/")
			if seg == "" || seg == "avsync" || seg == "pattern" || seg == "ipv6" {
				seg = "testcard.m3u8"
			} else if seg == "hdr" || seg == "hlg" || seg == "hdr10" || seg == "hdr_switch" {
				seg = "hdr_switch.m3u8"
			} else if seg == "hdr-smooth" || seg == "smooth" || seg == "hdr_smooth" {
				seg = "hdr_smooth.m3u8"
			}
			serveMediaFile(w, r, filepath.Join(getMediaDir("testcard"), seg))
			return
		}

		// 9. Twitch Group: twitch/group/<name> or group/<name>
		if strings.HasPrefix(path, "twitch/group/") || strings.HasPrefix(path, "group/") {
			group := strings.TrimPrefix(path, "twitch/group/")
			group = strings.TrimPrefix(group, "group/")
			streamURL, err := twitchMgr.ResolveGroup(r.Context(), group, bias)
			if err != nil || streamURL == "" {
				serveOfflineSlate(w, r)
				return
			}
			serveTwitchM3U8(w, r, streamURL, group)
			return
		}

		// 10. Twitch Game: twitch/game/<name> or game/<name>
		if strings.HasPrefix(path, "twitch/game/") || strings.HasPrefix(path, "game/") {
			game := strings.TrimPrefix(path, "twitch/game/")
			game = strings.TrimPrefix(game, "game/")
			streamURL, err := twitchMgr.ResolveGame(r.Context(), game, bias)
			if err != nil || streamURL == "" {
				serveOfflineSlate(w, r)
				return
			}
			serveTwitchM3U8(w, r, streamURL, game)
			return
		}

		// 11. Twitch Auto-Live
		if path == "twitch/auto-live" || path == "gaming/live" || path == "twitch/live" {
			streamURL, err := twitchMgr.Resolve(r.Context(), "speedrun")
			if err != nil || streamURL == "" {
				serveOfflineSlate(w, r)
				return
			}
			serveTwitchM3U8(w, r, streamURL, "speedrun")
			return
		}

		// 11b. Twitch Top Followed: twitch/followed/<rank>
		if strings.HasPrefix(path, "twitch/followed/") {
			rankStr := strings.TrimPrefix(path, "twitch/followed/")
			rankStr = strings.TrimSuffix(rankStr, ".m3u8")
			rank, _ := strconv.Atoi(rankStr)
			if rank < 1 {
				rank = 1
			}
			streamURL, err := twitchMgr.ResolveFollowedRank(r.Context(), rank)
			if err != nil || streamURL == "" {
				serveOfflineSlate(w, r)
				return
			}
			serveTwitchM3U8(w, r, streamURL, fmt.Sprintf("followed-%d", rank))
			return
		}

		// 12. Specific Twitch Channel: twitch/<channel>
		if strings.HasPrefix(path, "twitch/") {
			channel := strings.TrimPrefix(path, "twitch/")
			channel = strings.TrimSuffix(channel, ".m3u8")

			streamURL, err := twitchMgr.Resolve(r.Context(), channel)
			if err != nil || streamURL == "" {
				serveOfflineSlate(w, r)
				return
			}
			serveTwitchM3U8(w, r, streamURL, channel)
			return
		}

		// Fallback 404
		http.NotFound(w, r)
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", Port),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[Bridge] Starting Go IPTV Live Bridge on port %d...", Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Bridge] Server error: %v", err)
		}
	}()

	<-stopChan
	log.Printf("[Bridge] Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

func serveTwitchM3U8(w http.ResponseWriter, r *http.Request, streamURL, channel string) {
	m3u8, err := FetchAndMakeAbsoluteM3U8(r.Context(), streamURL)
	if err != nil {
		twitchMgr.Invalidate(channel)
		serveOfflineSlate(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(m3u8))
}

func serveOfflineSlate(w http.ResponseWriter, r *http.Request) {
	slatePath := filepath.Join(getMediaDir("offline"), "offline.m3u8")
	data, err := os.ReadFile(slatePath)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte("Channel is currently offline.\n"))
		return
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasSuffix(trimmed, ".ts") {
			out = append(out, "/iptv/offline/"+trimmed)
		} else {
			out = append(out, l)
		}
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(strings.Join(out, "\n")))
}

func serveMediaFile(w http.ResponseWriter, r *http.Request, filePath string) {
	if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if strings.HasSuffix(filePath, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	} else if strings.HasSuffix(filePath, ".ts") {
		w.Header().Set("Content-Type", "video/MP2T")
		w.Header().Set("Cache-Control", "max-age=60, public")
	}
	http.ServeFile(w, r, filePath)
}

func serveDistFile(w http.ResponseWriter, r *http.Request, filePath, fileName string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	if strings.HasSuffix(fileName, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(fileName, ".xml") {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	} else if strings.HasSuffix(fileName, ".gz") {
		w.Header().Set("Content-Type", "application/gzip")
	} else if strings.HasSuffix(fileName, ".json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	http.ServeFile(w, r, filePath)
}
