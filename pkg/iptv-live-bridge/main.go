package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ListenAddr  = "[fd00:2830::7555]:8080"
	Port        = 8080
	Host        = "fd00:2830::7555"
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
	if hEnv := os.Getenv("BRIDGE_HOST"); hEnv != "" {
		Host = hEnv
	}
	if pEnv := os.Getenv("BRIDGE_PORT"); pEnv != "" {
		if p, err := strconv.Atoi(pEnv); err == nil {
			Port = p
		}
	} else if pEnv := os.Getenv("PORT"); pEnv != "" {
		if p, err := strconv.Atoi(pEnv); err == nil {
			Port = p
		}
	}
	if aEnv := os.Getenv("BRIDGE_ADDR"); aEnv != "" {
		ListenAddr = aEnv
	} else {
		ListenAddr = net.JoinHostPort(Host, strconv.Itoa(Port))
	}

	buildDist := flag.Bool("build-dist", false, "Compile playlists and master EPG files from data/ into dist/ then exit")
	flag.StringVar(&ListenAddr, "listen", ListenAddr, "HTTP listen address ([host]:port)")
	flag.IntVar(&Port, "port", Port, "HTTP listen port")
	flag.StringVar(&Host, "host", Host, "HTTP listen host")
	flag.StringVar(&MediaDir, "media-dir", MediaDir, "Media root directory")
	flag.Parse()

	flagsSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})
	if flagsSet["listen"] {
		if h, p, err := net.SplitHostPort(ListenAddr); err == nil {
			Host = h
			if pInt, err := strconv.Atoi(p); err == nil {
				Port = pInt
			}
		}
	} else if flagsSet["port"] || flagsSet["host"] {
		ListenAddr = net.JoinHostPort(Host, strconv.Itoa(Port))
	}

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

	bvnEngine.SetListenAddr(ListenAddr)

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
				"version":   "4.4.1",
				"runtime":   "go",
				"timestamp": time.Now().Format(time.RFC3339),
			})
			return
		}

		// 2c. Gentle In-Memory Reload (flushes caches, reloads stations & metadata without dropping streams)
		if path == "reload" || path == "admin/reload" {
			reloadState(esperantoStation, bahaiStation)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"message":   "Gentle reload complete: caches flushed and linear stations refreshed without dropping streams",
				"timestamp": time.Now().Format(time.RFC3339),
			})
			return
		}

		// 2b. Real-Time Status & Now Playing Endpoints
		if path == "api/now" || path == "twitch/now.json" || path == "now.json" {
			serveNowJSON(w, r, esperantoStation, bahaiStation)
			return
		}
		if path == "now" || path == "twitch/now" || path == "now.html" {
			serveNowDashboard(w, r)
			return
		}
		if path == "overlay" || path == "twitch/overlay" {
			serveOverlayWidget(w, r)
			return
		}

		// 2c. Real-Time EPG Handlers
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

		// 4b. BVN Raw Unfiltered MPD (diagnostic endpoint for testing native player DASH & trickplay)
		if path == "test/bvn_raw.mpd" || path == "bvn_raw.mpd" || path == "bvn.mpd" || path == "bvn/manifest.mpd" {
			data, err := getBVNRawMPD(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/dash+xml")
			w.Header().Set("Access-Control-Allow-Origin", "*")
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

		// 7b. HLS Proxy Routes (/hls/m/..., /hls/s/..., /hls/manifest, /hls/segment)
		if strings.HasPrefix(path, "hls/m/") {
			parts := strings.Split(strings.TrimPrefix(path, "hls/m/"), "/")
			if len(parts) > 0 {
				HandleHLSManifest(w, r, parts[0])
				return
			}
		}
		if strings.HasPrefix(path, "hls/s/") {
			parts := strings.Split(strings.TrimPrefix(path, "hls/s/"), "/")
			if len(parts) > 0 {
				HandleHLSSegment(w, r, parts[0])
				return
			}
		}
		if path == "hls/manifest" || path == "hls/manifest.m3u8" {
			rawURL := r.URL.Query().Get("url")
			if rawURL != "" {
				token := base64.RawURLEncoding.EncodeToString([]byte(rawURL))
				HandleHLSManifest(w, r, token)
				return
			}
		}
		if path == "hls/segment" || path == "hls/segment.ts" {
			rawURL := r.URL.Query().Get("url")
			if rawURL != "" {
				token := base64.RawURLEncoding.EncodeToString([]byte(rawURL))
				HandleHLSSegment(w, r, token)
				return
			}
		}

		// 7c. Named Proxied Channels (ARTE, TV5Monde, ZDF, 3sat, Phoenix, KiKa, etc.)
		if ch, ok := ResolveProxiedChannel(path); ok {
			HandleProxiedChannel(w, r, ch)
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

		// 8b. Twitch Subtitle Segment: twitch/subseg/<target>/<seq>.vtt
		if strings.HasPrefix(path, "twitch/subseg/") {
			subPath := strings.TrimPrefix(path, "twitch/subseg/")
			subPath = strings.TrimSuffix(subPath, ".vtt")
			lastSlash := strings.LastIndex(subPath, "/")
			if lastSlash == -1 {
				http.NotFound(w, r)
				return
			}
			targetKey := subPath[:lastSlash]
			seqStr := subPath[lastSlash+1:]
			seq, _ := strconv.ParseInt(seqStr, 10, 64)
			serveTwitchSubSeg(w, r, targetKey, seq)
			return
		}

		// 8c. Twitch Subtitle Playlist: twitch/sub/<target>
		if strings.HasPrefix(path, "twitch/sub/") {
			target := strings.TrimPrefix(path, "twitch/sub/")
			serveTwitchSubM3U8(w, r, target, bias)
			return
		}

		// 8d. Twitch Video Media Playlist: twitch/video/<target>
		if strings.HasPrefix(path, "twitch/video/") {
			target := strings.TrimPrefix(path, "twitch/video/")
			streamURL, channelKey, err := resolveTwitchTarget(r.Context(), target, bias)
			if err != nil || streamURL == "" {
				serveOfflineSlate(w, r)
				return
			}
			serveTwitchM3U8(w, r, streamURL, channelKey)
			return
		}

		// 9. Twitch Group: twitch/group/<name> or group/<name>
		if strings.HasPrefix(path, "twitch/group/") || strings.HasPrefix(path, "group/") {
			group := strings.TrimPrefix(path, "twitch/group/")
			group = strings.TrimPrefix(group, "group/")
			if r.URL.Query().Get("raw") == "1" {
				streamURL, err := twitchMgr.ResolveGroup(r.Context(), group, bias)
				if err != nil || streamURL == "" {
					serveOfflineSlate(w, r)
					return
				}
				serveTwitchM3U8(w, r, streamURL, group)
				return
			}
			serveTwitchMasterM3U8(w, r, "group/"+group)
			return
		}

		// 10. Twitch Game: twitch/game/<name> or game/<name>
		if strings.HasPrefix(path, "twitch/game/") || strings.HasPrefix(path, "game/") {
			game := strings.TrimPrefix(path, "twitch/game/")
			game = strings.TrimPrefix(game, "game/")
			if r.URL.Query().Get("raw") == "1" {
				streamURL, err := twitchMgr.ResolveGame(r.Context(), game, bias)
				if err != nil || streamURL == "" {
					serveOfflineSlate(w, r)
					return
				}
				serveTwitchM3U8(w, r, streamURL, game)
				return
			}
			serveTwitchMasterM3U8(w, r, "game/"+game)
			return
		}

		// 11. Twitch Auto-Live
		if path == "twitch/auto-live" || path == "gaming/live" || path == "twitch/live" {
			if r.URL.Query().Get("raw") == "1" {
				streamURL, err := twitchMgr.Resolve(r.Context(), "speedrun")
				if err != nil || streamURL == "" {
					serveOfflineSlate(w, r)
					return
				}
				serveTwitchM3U8(w, r, streamURL, "speedrun")
				return
			}
			serveTwitchMasterM3U8(w, r, "speedrun")
			return
		}

		// 11b. Twitch Top Followed: twitch/followed/<rank>
		if strings.HasPrefix(path, "twitch/followed/") {
			rankStr := strings.TrimPrefix(path, "twitch/followed/")
			rankStr = strings.TrimSuffix(rankStr, ".m3u8")
			if r.URL.Query().Get("raw") == "1" {
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
			serveTwitchMasterM3U8(w, r, "followed/"+rankStr)
			return
		}

		// 12. Specific Twitch Channel: twitch/<channel>
		if strings.HasPrefix(path, "twitch/") {
			channel := strings.TrimPrefix(path, "twitch/")
			channel = strings.TrimSuffix(channel, ".m3u8")
			if r.URL.Query().Get("raw") == "1" {
				streamURL, err := twitchMgr.Resolve(r.Context(), channel)
				if err != nil || streamURL == "" {
					serveOfflineSlate(w, r)
					return
				}
				serveTwitchM3U8(w, r, streamURL, channel)
				return
			}
			serveTwitchMasterM3U8(w, r, channel)
			return
		}

		// Fallback 404
		http.NotFound(w, r)
	})

	listener, err := createListener(ListenAddr)
	if err != nil {
		log.Fatalf("[Bridge] Failed to create listener on %s: %v", ListenAddr, err)
	}

	recoveryMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[PANIC RECOVERED] path=%s remote=%s error=%v\nstack:\n%s",
						r.URL.Path, r.RemoteAddr, rec, debug.Stack())
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]any{
						"error":  "Internal server error",
						"detail": fmt.Sprintf("%v", rec),
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}

	server := &http.Server{
		Handler:      recoveryMiddleware(handler),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
	}

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		log.Printf("[Bridge] Starting Go IPTV Live Bridge on %s...", listener.Addr())
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Bridge] Server error: %v", err)
		}
	}()

	for sig := range sigChan {
		if sig == syscall.SIGHUP {
			log.Printf("[Bridge] SIGHUP received: performing gentle in-memory reload...")
			reloadState(esperantoStation, bahaiStation)
			continue
		}

		log.Printf("[Bridge] %v received: shutting down gracefully (allowing up to 30s for active streams to drain)...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("[Bridge] Graceful shutdown error: %v", err)
		}
		break
	}
}

func createListener(addr string) (net.Listener, error) {
	// 1. Check for systemd socket activation (zero-downtime socket passing)
	if listenFds := os.Getenv("LISTEN_FDS"); listenFds != "" {
		if n, err := strconv.Atoi(listenFds); err == nil && n > 0 {
			// In systemd socket activation, fd 3 is SD_LISTEN_FDS_START
			file := os.NewFile(3, "systemd-socket")
			if l, err := net.FileListener(file); err == nil {
				log.Printf("[Bridge] Activated via systemd socket on %s", l.Addr())
				return l, nil
			}
		}
	}

	// 2. Direct listen with SO_REUSEPORT & SO_REUSEADDR for gentle restarts
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error
			err := c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				// 0x0F is SO_REUSEPORT on Linux
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 0x0F, 1)
			})
			if err != nil {
				return err
			}
			return sockErr
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

func reloadState(esperantoStation, bahaiStation *LinearStation) {
	log.Printf("[Bridge] Reloading configurations, follows, and linear stations...")
	ReloadTwitchMetadata()
	if esperantoStation != nil {
		esperantoStation.Reload()
	}
	if bahaiStation != nil {
		bahaiStation.Reload()
	}

	twitchMgr.ClearCache()

	m3u8RecentMu.Lock()
	m3u8Recent = make(map[string]m3u8CacheEntry)
	m3u8RecentMu.Unlock()

	log.Printf("[Bridge] Gentle reload complete: 0 dropped connections.")
}

type m3u8CacheEntry struct {
	content   string
	timestamp time.Time
}

var (
	m3u8RecentMu sync.RWMutex
	m3u8Recent   = make(map[string]m3u8CacheEntry)
)

func serveTwitchM3U8(w http.ResponseWriter, r *http.Request, streamURL, channel string) {
	m3u8, err := FetchAndMakeAbsoluteM3U8(r.Context(), streamURL)
	if err != nil {
		twitchMgr.Invalidate(channel)

		// 1. Immediate fast retry: resolve fresh stream URL and fetch once more
		var freshURL string
		var rErr error
		if strings.HasPrefix(channel, "followed-") {
			rankStr := strings.TrimPrefix(channel, "followed-")
			rank, _ := strconv.Atoi(rankStr)
			freshURL, rErr = twitchMgr.ResolveFollowedRank(r.Context(), rank)
		} else if strings.HasPrefix(channel, "game:") {
			gameName := strings.TrimPrefix(channel, "game:")
			freshURL, rErr = twitchMgr.ResolveGame(r.Context(), gameName, "")
		} else {
			freshURL, rErr = twitchMgr.Resolve(r.Context(), channel)
		}

		if rErr == nil && freshURL != "" && freshURL != streamURL {
			m3u8, err = FetchAndMakeAbsoluteM3U8(r.Context(), freshURL)
		}
	}

	// 2. Resilience: If upstream attempts failed (transient CDN hiccup or network blip),
	// serve the previous playlist from recent cache (up to 6 seconds old).
	// This gives the player continuous buffer and completely eliminates "little black moments".
	if err != nil {
		m3u8RecentMu.RLock()
		cached, ok := m3u8Recent[channel]
		m3u8RecentMu.RUnlock()

		if ok && time.Since(cached.timestamp) < 6*time.Second {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Write([]byte(cached.content))
			return
		}

		serveOfflineSlate(w, r)
		return
	}

	// Enrich playlist with real-time active streamer metadata if available
	if sInfo := twitchMgr.GetActiveStreamInfo(channel); sInfo != nil && sInfo.DisplayName != "" {
		tag := fmt.Sprintf("🔴 %s", sInfo.DisplayName)
		if sInfo.Game != "" {
			tag += fmt.Sprintf(" • %s", sInfo.Game)
		}
		if sInfo.Viewers > 0 {
			tag += fmt.Sprintf(" (%d viewers)", sInfo.Viewers)
		}
		m3u8 = strings.ReplaceAll(m3u8, ",live", ","+tag)
		w.Header().Set("X-Streamer-Name", sInfo.DisplayName)
		w.Header().Set("X-Streamer-Game", sInfo.Game)
		if sInfo.Viewers > 0 {
			w.Header().Set("X-Streamer-Viewers", strconv.Itoa(sInfo.Viewers))
		}
	}

	// Update recent cache on success
	m3u8RecentMu.Lock()
	m3u8Recent[channel] = m3u8CacheEntry{
		content:   m3u8,
		timestamp: time.Now(),
	}
	m3u8RecentMu.Unlock()

	UpdateTwitchSubState(channel, m3u8, streamURL)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(m3u8))
}

func resolveTwitchTarget(ctx context.Context, target, bias string) (string, string, error) {
	clean := strings.Trim(target, "/")
	clean = strings.TrimSuffix(clean, ".m3u8")

	if strings.HasPrefix(clean, "followed/") {
		rankStr := strings.TrimPrefix(clean, "followed/")
		rank, _ := strconv.Atoi(rankStr)
		if rank < 1 {
			rank = 1
		}
		u, err := twitchMgr.ResolveFollowedRank(ctx, rank)
		return u, fmt.Sprintf("followed-%d", rank), err
	}

	if strings.HasPrefix(clean, "group/") {
		group := strings.TrimPrefix(clean, "group/")
		u, err := twitchMgr.ResolveGroup(ctx, group, bias)
		return u, group, err
	}

	if strings.HasPrefix(clean, "game/") {
		game := strings.TrimPrefix(clean, "game/")
		u, err := twitchMgr.ResolveGame(ctx, game, bias)
		return u, game, err
	}

	if clean == "auto-live" || clean == "live" || clean == "speedrun" {
		u, err := twitchMgr.Resolve(ctx, "speedrun")
		return u, "speedrun", err
	}

	u, err := twitchMgr.Resolve(ctx, clean)
	return u, clean, err
}

func serveTwitchMasterM3U8(w http.ResponseWriter, r *http.Request, target string) {
	q := r.URL.RawQuery
	queryString := ""
	if q != "" {
		queryString = "?" + q
	}

	cleanTarget := strings.Trim(target, "/")
	cleanTarget = strings.TrimSuffix(cleanTarget, ".m3u8")

	subURI := fmt.Sprintf("/iptv/twitch/sub/%s.m3u8%s", cleanTarget, queryString)
	videoURI := fmt.Sprintf("/iptv/twitch/video/%s.m3u8%s", cleanTarget, queryString)

	master := fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:4
#EXT-X-INDEPENDENT-SEGMENTS

#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Stream Info",DEFAULT=YES,AUTOSELECT=YES,FORCED=YES,LANGUAGE="en",URI="%s"

#EXT-X-STREAM-INF:BANDWIDTH=6000000,AVERAGE-BANDWIDTH=4000000,RESOLUTION=1920x1080,FRAME-RATE=60.000,SUBTITLES="subs"
%s
`, subURI, videoURI)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(master))
}

func serveTwitchSubM3U8(w http.ResponseWriter, r *http.Request, target, bias string) {
	channelKey := normalizeChannelKey(target)
	st := GetTwitchSubState(channelKey)

	if st == nil || time.Since(st.LastUpdated) > 3*time.Second {
		streamURL, key, err := resolveTwitchTarget(r.Context(), target, bias)
		if err == nil && streamURL != "" {
			m3u8, fErr := FetchAndMakeAbsoluteM3U8(r.Context(), streamURL)
			if fErr == nil {
				UpdateTwitchSubState(key, m3u8, streamURL)
				st = GetTwitchSubState(key)
			}
		}
	}

	if st == nil || len(st.Segments) == 0 {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:6.000,\n/iptv/twitch/subseg/offline/0.vtt\n#EXT-X-ENDLIST\n"))
		return
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", st.TargetDuration))
	b.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n\n", st.MediaSequence))

	cleanTarget := strings.Trim(target, "/")
	cleanTarget = strings.TrimSuffix(cleanTarget, ".m3u8")

	for _, seg := range st.Segments {
		b.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", seg.Duration))
		b.WriteString(fmt.Sprintf("/iptv/twitch/subseg/%s/%d.vtt\n", cleanTarget, seg.Seq))
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(b.String()))
}

func serveTwitchSubSeg(w http.ResponseWriter, r *http.Request, targetKey string, seq int64) {
	if targetKey == "offline" {
		vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:06.000 line:85% align:center\n⚠️ Channel is currently offline\n"
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "max-age=10")
		w.Write([]byte(vtt))
		return
	}

	channelKey := normalizeChannelKey(targetKey)
	st := GetTwitchSubState(channelKey)

	var pts uint64
	duration := 2.0
	if st != nil {
		found := false
		for _, seg := range st.Segments {
			if seg.Seq == seq {
				pts = seg.PTS
				duration = seg.Duration
				found = true
				break
			}
		}
		if !found {
			pts = st.BasePTS + uint64(float64(seq-st.BaseSeq)*2.0*90000.0)
		}
	} else {
		pts = uint64(time.Now().Unix()%86400) * 90000
	}

	sInfo := twitchMgr.GetActiveStreamInfo(channelKey)
	if sInfo == nil {
		sInfo = twitchMgr.GetActiveStreamInfo(targetKey)
	}

	text := "🔴 Live: Twitch"
	if sInfo != nil && sInfo.DisplayName != "" {
		text = fmt.Sprintf("🔴 Live: %s", sInfo.DisplayName)
		if sInfo.Game != "" {
			text += fmt.Sprintf(" • %s", sInfo.Game)
		}
		if sInfo.Viewers > 0 {
			text += fmt.Sprintf(" (%s viewers)", formatNumber(sInfo.Viewers))
		}
	} else if channelKey != "" && channelKey != "speedrun" {
		text = fmt.Sprintf("🔴 Live: %s", channelKey)
	}

	vtt := fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:%d,LOCAL:00:00:00.000\n\n00:00:00.000 --> %s line:85%% align:center\n%s\n",
		pts, formatVTTTime(duration), text)

	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "max-age=10")
	w.Write([]byte(vtt))
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

func serveNowJSON(w http.ResponseWriter, r *http.Request, esp, bahai *LinearStation) {
	liveFollows := twitchMgr.GetRankedLiveFollows(r.Context())

	type ChannelNow struct {
		ChNo        int    `json:"chno"`
		Slot        string `json:"slot"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Live        bool   `json:"live"`
		Streamer    string `json:"streamer"`
		DisplayName string `json:"display_name"`
		Game        string `json:"game"`
		Title       string `json:"title"`
		Viewers     int    `json:"viewers"`
		StreamURL   string `json:"stream_url"`
	}

	var channels []ChannelNow

	// 1. Followed Streamers (Ch. 320–329)
	for i := 1; i <= 10; i++ {
		slotKey := fmt.Sprintf("followed-%d", i)
		sInfo := twitchMgr.GetActiveStreamInfo(slotKey)

		item := ChannelNow{
			ChNo:      319 + i,
			Slot:      slotKey,
			Name:      fmt.Sprintf("Followed Streamer #%d", i),
			Category:  "LaPingvino Favorites",
			StreamURL: fmt.Sprintf("/iptv/twitch/followed/%d", i),
		}

		if sInfo != nil && sInfo.DisplayName != "" {
			item.Live = true
			item.Streamer = sInfo.Login
			item.DisplayName = sInfo.DisplayName
			item.Game = sInfo.Game
			item.Title = sInfo.Title
			item.Viewers = sInfo.Viewers
		} else if i-1 < len(liveFollows) {
			item.Live = true
			item.Streamer = liveFollows[i-1].Login
			item.DisplayName = liveFollows[i-1].DisplayName
			item.Game = liveFollows[i-1].Game
			item.Title = liveFollows[i-1].Title
			item.Viewers = liveFollows[i-1].Viewers
		}

		channels = append(channels, item)
	}

	// 2. Dedicated Gaming Channels
	gamingChannels := []struct {
		Slot string
		ChNo int
		Name string
	}{
		{"speedrun", 250, "Speedrun (24/7 Speedrun.com)"},
		{"gamesdonequick", 251, "GamesDoneQuick (GDQ)"},
		{"esamarathon", 252, "ESAMarathon"},
		{"tasvideos", 253, "TASVideos"},
		{"mitchflowerpower", 254, "MitchFlowerPower (SMB3)"},
		{"classictetris", 285, "Classic Tetris (CTWC Main)"},
		{"classictetris2", 286, "Classic Tetris 2 (CTWC)"},
	}
	for _, g := range gamingChannels {
		sInfo := twitchMgr.GetActiveStreamInfo(g.Slot)
		item := ChannelNow{
			ChNo:      g.ChNo,
			Slot:      g.Slot,
			Name:      g.Name,
			Category:  "Gaming",
			StreamURL: fmt.Sprintf("/iptv/twitch/%s", g.Slot),
		}
		if sInfo != nil && sInfo.DisplayName != "" {
			item.Live = true
			item.Streamer = sInfo.Login
			item.DisplayName = sInfo.DisplayName
			item.Game = sInfo.Game
			item.Title = sInfo.Title
			item.Viewers = sInfo.Viewers
		}
		channels = append(channels, item)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	json.NewEncoder(w).Encode(map[string]any{
		"timestamp": time.Now().Format(time.RFC3339),
		"channels":  channels,
	})
}

func serveNowDashboard(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LaPingvino IPTV • Live Stream Status & Now Playing</title>
<style>
  :root {
    --bg: #0b0f19;
    --card: #151d30;
    --card-hover: #1e2942;
    --border: rgba(255,255,255,0.08);
    --text: #f1f5f9;
    --subtext: #94a3b8;
    --primary: #9146ff;
    --live: #10b981;
    --standby: #64748b;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: var(--bg);
    color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Oxygen, Ubuntu, sans-serif;
    padding: 32px 20px;
    max-width: 1280px;
    margin: 0 auto;
  }
  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 16px;
    margin-bottom: 32px;
    padding-bottom: 20px;
    border-bottom: 1px solid var(--border);
  }
  h1 { font-size: 26px; font-weight: 700; display: flex; align-items: center; gap: 10px; }
  .badge-live {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 12px;
    background: rgba(16, 185, 129, 0.15);
    border: 1px solid rgba(16, 185, 129, 0.3);
    color: var(--live);
    border-radius: 9999px;
    font-size: 13px;
    font-weight: 600;
  }
  .pulse {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--live);
    box-shadow: 0 0 8px var(--live);
    animation: pulse 1.8s infinite;
  }
  @keyframes pulse {
    0% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.3); opacity: 0.5; }
    100% { transform: scale(1); opacity: 1; }
  }
  .section-title {
    font-size: 19px;
    font-weight: 600;
    margin: 28px 0 16px;
    color: #e2e8f0;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 16px;
  }
  .card {
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 16px;
    transition: transform 0.2s, border-color 0.2s;
    position: relative;
    overflow: hidden;
  }
  .card:hover {
    transform: translateY(-2px);
    border-color: rgba(145, 70, 255, 0.4);
  }
  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 12px;
  }
  .chno {
    font-size: 12px;
    font-weight: 700;
    color: var(--primary);
    background: rgba(145, 70, 255, 0.12);
    padding: 2px 8px;
    border-radius: 6px;
    letter-spacing: 0.5px;
  }
  .status-tag {
    font-size: 11px;
    font-weight: 700;
    padding: 2px 8px;
    border-radius: 9999px;
    text-transform: uppercase;
  }
  .status-live {
    background: rgba(16, 185, 129, 0.2);
    color: var(--live);
  }
  .status-standby {
    background: rgba(100, 116, 139, 0.2);
    color: var(--standby);
  }
  .streamer-name {
    font-size: 17px;
    font-weight: 700;
    margin-bottom: 4px;
    color: #fff;
  }
  .streamer-name a {
    color: inherit;
    text-decoration: none;
  }
  .streamer-name a:hover {
    color: var(--primary);
  }
  .game-name {
    font-size: 13px;
    color: #cbd5e1;
    margin-bottom: 8px;
    display: inline-block;
    background: rgba(255,255,255,0.06);
    padding: 2px 8px;
    border-radius: 4px;
  }
  .stream-title {
    font-size: 12px;
    color: var(--subtext);
    margin-bottom: 14px;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    height: 34px;
  }
  .card-footer {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding-top: 10px;
    border-top: 1px solid var(--border);
    font-size: 12px;
  }
  .viewers {
    color: #e2e8f0;
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .links {
    display: flex;
    gap: 8px;
  }
  .links a {
    color: var(--primary);
    text-decoration: none;
    font-weight: 600;
    font-size: 12px;
  }
  .links a:hover { text-decoration: underline; }
</style>
</head>
<body>
  <header>
    <div>
      <h1>📺 LaPingvino IPTV</h1>
      <p style="color:var(--subtext); margin-top:4px; font-size:14px;">Live Streamer Monitor & Now Playing Guide</p>
    </div>
    <div style="display:flex; align-items:center; gap:12px;">
      <span class="badge-live"><span class="pulse"></span> AUTO-REFRESHING</span>
      <span id="clock" style="font-size:13px; color:var(--subtext); font-variant-numeric:tabular-nums;"></span>
    </div>
  </header>

  <h2 class="section-title">⭐ LaPingvino Favorites (Top 10 Live Follows • Ch. 320–329)</h2>
  <div class="grid" id="favorites-grid">
    <p style="color:var(--subtext);">Loading live follows...</p>
  </div>

  <h2 class="section-title">🎮 Dedicated Gaming Streams (Ch. 250–286)</h2>
  <div class="grid" id="gaming-grid">
    <p style="color:var(--subtext);">Loading gaming streams...</p>
  </div>

  <script>
    function updateClock() {
      const now = new Date();
      document.getElementById('clock').textContent = now.toLocaleTimeString();
    }
    setInterval(updateClock, 1000);
    updateClock();

    async function refreshNow() {
      try {
        const res = await fetch('/api/now');
        const data = await res.json();

        const favsGrid = document.getElementById('favorites-grid');
        const gamingGrid = document.getElementById('gaming-grid');

        const favs = data.channels.filter(c => c.category === 'LaPingvino Favorites');
        const gaming = data.channels.filter(c => c.category === 'Gaming');

        favsGrid.innerHTML = favs.map(renderCard).join('');
        gamingGrid.innerHTML = gaming.map(renderCard).join('');
      } catch (err) {
        console.error('Failed to load /api/now:', err);
      }
    }

    function renderCard(ch) {
      const isLive = ch.live;
      const statusClass = isLive ? 'status-live' : 'status-standby';
      const statusText = isLive ? 'LIVE' : 'STANDBY';
      const name = isLive ? (ch.display_name || ch.streamer) : ch.name;
      const game = isLive ? (ch.game || 'Gaming') : 'Slot Available';
      const title = isLive ? (ch.title || 'Live Broadcast') : 'Awaiting live followed streamer...';
      const twitchUrl = isLive ? 'https://twitch.tv/' + ch.streamer : '#';
      const viewers = isLive && ch.viewers > 0 ? ch.viewers.toLocaleString() + ' viewers' : '—';

      return '<div class="card">' +
        '<div class="card-header">' +
          '<span class="chno">CH ' + ch.chno + '</span>' +
          '<span class="status-tag ' + statusClass + '">' + statusText + '</span>' +
        '</div>' +
        '<div class="streamer-name">' +
          (isLive ? '<a href="' + twitchUrl + '" target="_blank" rel="noopener">' + name + '</a>' : name) +
        '</div>' +
        '<div class="game-name">' + game + '</div>' +
        '<div class="stream-title">' + title + '</div>' +
        '<div class="card-footer">' +
          '<span class="viewers">👁️ ' + viewers + '</span>' +
          '<div class="links">' +
            '<a href="' + ch.stream_url + '" target="_blank">Stream</a>' +
            '<a href="/overlay?channel=' + ch.slot + '" target="_blank">Overlay</a>' +
          '</div>' +
        '</div>' +
      '</div>';
    }

    setInterval(refreshNow, 10000);
    refreshNow();
  </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Write([]byte(html))
}

func serveOverlayWidget(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>IPTV Overlay Widget</title>
<style>
  body {
    margin: 0;
    padding: 24px;
    background: transparent;
    overflow: hidden;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 12px;
    padding: 10px 20px;
    background: rgba(15, 23, 42, 0.88);
    backdrop-filter: blur(12px);
    border: 1px solid rgba(145, 70, 255, 0.5);
    border-radius: 9999px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
    color: #fff;
    font-size: 16px;
    font-weight: 600;
    transition: all 0.4s ease;
  }
  .pulse {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: #ef4444;
    box-shadow: 0 0 10px #ef4444;
    animation: pulse 1.5s infinite;
  }
  @keyframes pulse {
    0% { opacity: 1; transform: scale(1); }
    50% { opacity: 0.4; transform: scale(1.2); }
    100% { opacity: 1; transform: scale(1); }
  }
  .name { color: #f8fafc; font-weight: 700; }
  .sep { color: rgba(255,255,255,0.3); }
  .game { color: #a78bfa; font-weight: 500; }
  .viewers {
    background: rgba(255,255,255,0.1);
    padding: 3px 8px;
    border-radius: 6px;
    font-size: 13px;
    color: #cbd5e1;
  }
  .hidden { opacity: 0; transform: translateY(15px); }
</style>
</head>
<body>
  <div id="badge" class="badge">
    <span class="pulse"></span>
    <span class="name" id="name">Loading...</span>
    <span class="sep">•</span>
    <span class="game" id="game">Twitch Live</span>
    <span class="viewers" id="viewers">...</span>
  </div>
  <script>
    const params = new URLSearchParams(window.location.search);
    const slot = params.get('channel') || params.get('slot') || 'followed-1';
    async function update() {
      try {
        const res = await fetch('/api/now');
        const data = await res.json();
        const ch = data.channels.find(c => c.slot === slot || String(c.chno) === slot);
        if (ch && ch.live) {
          document.getElementById('name').textContent = ch.display_name || ch.streamer;
          document.getElementById('game').textContent = ch.game || 'Live Stream';
          document.getElementById('viewers').textContent = ch.viewers ? ch.viewers.toLocaleString() + ' viewers' : 'LIVE';
          document.getElementById('badge').classList.remove('hidden');
        } else {
          document.getElementById('badge').classList.add('hidden');
        }
      } catch(e) {}
    }
    setInterval(update, 4000);
    update();
  </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Write([]byte(html))
}

