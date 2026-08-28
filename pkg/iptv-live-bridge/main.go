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
)

func getMediaDir(sub string) string {
	p1 := filepath.Join(MediaDir, sub)
	if _, err := os.Stat(p1); err == nil {
		return p1
	}
	return filepath.Join(FallbackDir, sub)
}

func main() {
	if pEnv := os.Getenv("PORT"); pEnv != "" {
		if p, err := strconv.Atoi(pEnv); err == nil {
			Port = p
		}
	}

	flag.IntVar(&Port, "port", Port, "HTTP listen port")
	flag.StringVar(&MediaDir, "media-dir", MediaDir, "Media root directory")
	flag.Parse()

	bvnEngine.SetPort(Port)

	esperantoDir := getMediaDir("esperantotv")
	bahaiDir := getMediaDir("bahaitv")

	esperantoStation := NewLinearStation(esperantoDir, "esperanto", 10.0)
	bahaiStation := NewLinearStation(bahaiDir, "bahai", 8.333333)

	mux := http.NewServeMux()

	// 1. BVN Routes
	mux.HandleFunc("/bvn_internal.mpd", func(w http.ResponseWriter, r *http.Request) {
		data, err := getBVNDynamicMPD(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/dash+xml")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Write(data)
	})

	handleBVNStream := func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/bvn", handleBVNStream)
	mux.HandleFunc("/bvn.ts", handleBVNStream)
	mux.HandleFunc("/nl/bvn", handleBVNStream)
	mux.HandleFunc("/nl/bvn.ts", handleBVNStream)

	// 2. Linear Stations (Esperanto TV & Bahá'í TV)
	handleEsperanto := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write([]byte(esperantoStation.Playlist("/iptv/testcard/esperanto_standby0.ts")))
	}
	mux.HandleFunc("/esperanto", handleEsperanto)
	mux.HandleFunc("/esperanto.m3u8", handleEsperanto)
	mux.HandleFunc("/esperantotv", handleEsperanto)
	mux.HandleFunc("/esperantotv.m3u8", handleEsperanto)

	handleBahai := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write([]byte(bahaiStation.Playlist("/iptv/testcard/bahai_standby0.ts")))
	}
	mux.HandleFunc("/bahai", handleBahai)
	mux.HandleFunc("/bahai.m3u8", handleBahai)
	mux.HandleFunc("/bahaitv", handleBahai)
	mux.HandleFunc("/bahaitv.m3u8", handleBahai)

	// 3. Disney Channel Fast Proxy
	handleDisney := func(w http.ResponseWriter, r *http.Request) {
		upstreamURL := "http://151.80.18.177:86/Disney_Channel_HD/tracks-v1a1/mono.m3u8"
		m3u8, err := FetchAndMakeAbsoluteM3U8(r.Context(), upstreamURL)
		if err != nil {
			http.Error(w, "Disney upstream error: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write([]byte(m3u8))
	}
	mux.HandleFunc("/disney", handleDisney)
	mux.HandleFunc("/disney.m3u8", handleDisney)

	// 4. Segment Serving (/iptv/esperanto/*, /iptv/bahai/*, /iptv/testcard/*)
	mux.HandleFunc("/iptv/esperanto/", func(w http.ResponseWriter, r *http.Request) {
		seg := strings.TrimPrefix(r.URL.Path, "/iptv/esperanto/")
		serveSegment(w, r, filepath.Join(esperantoDir, seg))
	})
	mux.HandleFunc("/iptv/bahai/", func(w http.ResponseWriter, r *http.Request) {
		seg := strings.TrimPrefix(r.URL.Path, "/iptv/bahai/")
		serveSegment(w, r, filepath.Join(bahaiDir, seg))
	})
	mux.HandleFunc("/iptv/testcard/", func(w http.ResponseWriter, r *http.Request) {
		seg := strings.TrimPrefix(r.URL.Path, "/iptv/testcard/")
		serveSegment(w, r, filepath.Join(getMediaDir("testcard"), seg))
	})

	// 5. Twitch Route (/twitch/<channel>)
	mux.HandleFunc("/twitch/", func(w http.ResponseWriter, r *http.Request) {
		channel := strings.TrimPrefix(r.URL.Path, "/twitch/")
		channel = strings.TrimSuffix(channel, ".m3u8")

		streamURL, err := twitchMgr.Resolve(r.Context(), channel)
		if err != nil {
			http.Error(w, fmt.Sprintf("Stream offline or unavailable: %v", err), http.StatusNotFound)
			return
		}

		m3u8, err := FetchAndMakeAbsoluteM3U8(r.Context(), streamURL)
		if err != nil {
			twitchMgr.Invalidate(channel)
			http.Error(w, fmt.Sprintf("Failed to fetch stream manifest: %v", err), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write([]byte(m3u8))
	})

	// 6. Status / Health
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		status := map[string]any{
			"status":    "online",
			"runtime":   "go",
			"port":      Port,
			"timestamp": time.Now().Format(time.RFC3339),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // Unbounded for live streams
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

func serveSegment(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "max-age=60, public")
	http.ServeFile(w, r, path)
}
