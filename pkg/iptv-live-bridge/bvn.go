package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	BVNDecryptionKey = "8fdccd948bb2cc6d99d5305ccffebcb7"
)

var (
	jwtRegex = regexp.MustCompile(`let jwtnpoplayer[a-zA-Z0-9]+\s*=\s*"([^"]+)"`)
)

type BVNMPDCache struct {
	mu     sync.RWMutex
	url    string
	urlTS  time.Time
	xml    []byte
	xmlTS  time.Time
}

var bvnCache BVNMPDCache

func getBVNStreamURL(ctx context.Context) (string, error) {
	bvnCache.mu.RLock()
	if bvnCache.url != "" && time.Since(bvnCache.urlTS) < 600*time.Second {
		u := bvnCache.url
		bvnCache.mu.RUnlock()
		return u, nil
	}
	bvnCache.mu.RUnlock()

	bvnCache.mu.Lock()
	defer bvnCache.mu.Unlock()

	// Double-check after acquiring write lock
	if bvnCache.url != "" && time.Since(bvnCache.urlTS) < 600*time.Second {
		return bvnCache.url, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.bvn.tv/tv-gids/?player=live", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch BVN live page: %w", err)
	}
	defer resp.Body.Close()

	htmlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	m := jwtRegex.FindSubmatch(htmlBytes)
	if len(m) < 2 {
		return "", errors.New("failed to extract dynamic JWT from bvn.tv")
	}
	jwtToken := string(m[1])

	payload := map[string]any{
		"profileName": "dash",
		"drmType":     "widevine",
		"referrerUrl": "https://www.bvn.tv/tv-gids/?player=live",
		"ster":        map[string]string{"identifier": "npo"},
	}
	pBytes, _ := json.Marshal(payload)

	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://prod.npoplayer.nl/stream-link", bytes.NewReader(pBytes))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Authorization", jwtToken)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Origin", "https://www.bvn.tv")
	req2.Header.Set("User-Agent", "Mozilla/5.0")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", fmt.Errorf("failed to request stream-link: %w", err)
	}
	defer resp2.Body.Close()

	var apiRes struct {
		Stream struct {
			StreamURL string `json:"streamURL"`
		} `json:"stream"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&apiRes); err != nil {
		return "", err
	}

	mpdURL := apiRes.Stream.StreamURL
	if mpdURL == "" {
		return "", errors.New("no streamURL returned from npoplayer API")
	}

	bvnCache.url = mpdURL
	bvnCache.urlTS = time.Now()
	log.Printf("[BVN] Resolved fresh CDN MPD: %s...", mpdURL[:60])
	return mpdURL, nil
}

func getBVNDynamicMPD(ctx context.Context) ([]byte, error) {
	bvnCache.mu.RLock()
	if len(bvnCache.xml) > 0 && time.Since(bvnCache.xmlTS) < 1500*time.Millisecond {
		data := bvnCache.xml
		bvnCache.mu.RUnlock()
		return data, nil
	}
	bvnCache.mu.RUnlock()

	mpdURL, err := getBVNStreamURL(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mpdURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawXML, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	xmlStr := string(rawXML)
	baseURL := mpdURL[:strings.LastIndex(mpdURL, "/")+1]

	// Always insert root BaseURL before <Period so ffmpeg resolves dash/ chunks against CDN
	xmlStr = strings.Replace(xmlStr, "<Period", fmt.Sprintf("<BaseURL>%s</BaseURL><Period", baseURL), 1)

	// Adjust suggestedPresentationDelay and minBufferTime for stable live buffering
	if !strings.Contains(xmlStr, "suggestedPresentationDelay=") {
		xmlStr = strings.Replace(xmlStr, "<MPD", `<MPD suggestedPresentationDelay="PT8S"`, 1)
	} else {
		xmlStr = regexp.MustCompile(`suggestedPresentationDelay="[^"]*"`).ReplaceAllString(xmlStr, `suggestedPresentationDelay="PT8S"`)
	}
	xmlStr = regexp.MustCompile(`minBufferTime="[^"]*"`).ReplaceAllString(xmlStr, `minBufferTime="PT4S"`)

	// 1. Remove trick-play AdaptationSets (codingDependency="false" or maxPlayoutRate)
	reTrick := regexp.MustCompile(`(?s)<AdaptationSet[^>]*(?:codingDependency="false"|maxPlayoutRate="[^"]*")[^>]*>.*?</AdaptationSet>`)
	xmlStr = reTrick.ReplaceAllString(xmlStr, "")

	// 2. Remove subtitle/text AdaptationSets
	reText := regexp.MustCompile(`(?s)<AdaptationSet[^>]*contentType="text"[^>]*>.*?</AdaptationSet>`)
	xmlStr = reText.ReplaceAllString(xmlStr, "")

	// 3. Dynamically filter video representations to keep only the highest bandwidth
	xmlStr = filterDynamicVideoAdaptationSets(xmlStr)

	data := []byte(xmlStr)
	bvnCache.mu.Lock()
	bvnCache.xml = data
	bvnCache.xmlTS = time.Now()
	bvnCache.mu.Unlock()

	return data, nil
}

// getBVNRawMPD returns the unstripped, full-fidelity live DASH MPD with absolute BaseURL,
// preserving all ABR tiers, subtitles, and trick-play scrubbing tracks for testing native player DASH support.
func getBVNRawMPD(ctx context.Context) ([]byte, error) {
	mpdURL, err := getBVNStreamURL(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mpdURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawXML, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	xmlStr := string(rawXML)
	baseURL := mpdURL[:strings.LastIndex(mpdURL, "/")+1]
	xmlStr = strings.Replace(xmlStr, "<Period", fmt.Sprintf("<BaseURL>%s</BaseURL><Period", baseURL), 1)

	return []byte(xmlStr), nil
}

// filterDynamicVideoAdaptationSets dynamically keeps only the single representation with the highest bandwidth,
// completely eliminating fragile hardcoded bitrates.
func filterDynamicVideoAdaptationSets(xmlStr string) string {
	reVideoSet := regexp.MustCompile(`(?s)<AdaptationSet[^>]*contentType="video"[^>]*>.*?</AdaptationSet>`)
	return reVideoSet.ReplaceAllStringFunc(xmlStr, func(adSet string) string {
		reRep := regexp.MustCompile(`(?s)<Representation\s+([^>]+)>.*?</Representation>`)
		reBandwidth := regexp.MustCompile(`bandwidth="(\d+)"`)

		maxBW := -1
		var bestRep string

		matches := reRep.FindAllStringSubmatch(adSet, -1)
		for _, m := range matches {
			bw := 0
			if bwMatch := reBandwidth.FindStringSubmatch(m[1]); len(bwMatch) >= 2 {
				bw, _ = strconv.Atoi(bwMatch[1])
			}
			if bw > maxBW {
				maxBW = bw
				bestRep = m[0]
			}
		}

		if bestRep != "" {
			return reRep.ReplaceAllStringFunc(adSet, func(raw string) string {
				if raw == bestRep {
					return raw
				}
				return ""
			})
		}
		return adSet
	})
}

// BVNEngine manages the single-instance ffmpeg decryption process with goroutine fan-out.
type BVNEngine struct {
	mu           sync.Mutex
	running      bool
	cancel       context.CancelFunc
	clients      map[chan []byte]struct{}
	recentChunks [][]byte
	lastAccess   time.Time
	port         int
	listenAddr   string
}

var bvnEngine = &BVNEngine{
	clients:    make(map[chan []byte]struct{}),
	port:       8080,
	listenAddr: "[fd00:2830::7555]:8080",
}

func (e *BVNEngine) SetListenAddr(addr string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listenAddr = addr
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if pInt, err := strconv.Atoi(p); err == nil {
			e.port = pInt
		}
	}
}

func (e *BVNEngine) SetPort(p int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.port = p
	h, _, err := net.SplitHostPort(e.listenAddr)
	if err != nil || h == "" {
		h = Host
	}
	e.listenAddr = net.JoinHostPort(h, strconv.Itoa(p))
}

func (e *BVNEngine) Subscribe() chan []byte {
	ch := make(chan []byte, 256)
	e.mu.Lock()
	defer e.mu.Unlock()

	e.lastAccess = time.Now()
	e.clients[ch] = struct{}{}

	if !e.running {
		e.startWorker()
	} else {
		// Send recent burst buffer
		for _, c := range e.recentChunks {
			select {
			case ch <- c:
			default:
			}
		}
	}
	return ch
}

func (e *BVNEngine) Unsubscribe(ch chan []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.clients[ch]; ok {
		delete(e.clients, ch)
		close(ch)
	}
	e.lastAccess = time.Now()
}

func (e *BVNEngine) startWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.running = true
	e.recentChunks = nil

	e.mu.Lock()
	targetAddr := e.listenAddr
	e.mu.Unlock()

	if targetAddr == "" {
		targetAddr = ListenAddr
	}

	host, port, err := net.SplitHostPort(targetAddr)
	var connectHost string
	if err == nil {
		if host == "" || host == "0.0.0.0" {
			connectHost = "127.0.0.1"
		} else if host == "::" {
			connectHost = "[::1]"
		} else {
			if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
				connectHost = "[" + host + "]"
			} else {
				connectHost = host
			}
		}
	} else {
		connectHost = "127.0.0.1"
		port = strconv.Itoa(Port)
	}
	mpdURL := fmt.Sprintf("http://%s:%s/bvn_internal.mpd", connectHost, port)

	cmd := exec.CommandContext(
		ctx,
		"ffmpeg", "-nostdin", "-v", "warning",
		"-re",
		"-cenc_decryption_key", BVNDecryptionKey,
		"-i", mpdURL,
		"-map", "0:v:0",
		"-map", "0:a:0",
		"-c:v", "copy",
		"-bsf:v", "h264_mp4toannexb",
		"-c:a", "aac",
		"-b:a", "160k",
		"-af", "aresample=async=1",
		"-avoid_negative_ts", "make_zero",
		"-mpegts_flags", "resend_headers+initial_discontinuity",
		"-f", "mpegts",
		"pipe:1",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[BVN] Failed to open ffmpeg pipe: %v", err)
		e.running = false
		cancel()
		return
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("[BVN] Failed to start ffmpeg worker: %v", err)
		e.running = false
		cancel()
		return
	}

	log.Printf("[BVN] Started native Widevine decryption worker (PID %d)", cmd.Process.Pid)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[BVN PANIC RECOVERED] %v", rec)
			}
			stdout.Close()
			if waitErr := cmd.Wait(); waitErr != nil {
				log.Printf("[BVN] FFmpeg process exited: %v", waitErr)
			}
			cancel()
			e.mu.Lock()
			e.running = false
			for c := range e.clients {
				close(c)
			}
			e.clients = make(map[chan []byte]struct{})
			e.mu.Unlock()
			log.Printf("[BVN] Decryption worker stopped and clients notified")
		}()

		buf := make([]byte, 65536)
		dataChan := make(chan []byte)

		// Sub-goroutine reading from pipe
		go func() {
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					dataChan <- chunk
				}
				if err != nil {
					close(dataChan)
					return
				}
			}
		}()

		lastData := time.Now()

		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-dataChan:
				if !ok {
					return
				}
				lastData = time.Now()
				e.mu.Lock()
				if len(e.recentChunks) >= 16 {
					e.recentChunks = e.recentChunks[1:]
				}
				e.recentChunks = append(e.recentChunks, chunk)

				for ch := range e.clients {
					select {
					case ch <- chunk:
					default:
					}
				}
				e.mu.Unlock()

			case <-time.After(1 * time.Second):
				e.mu.Lock()
				numClients := len(e.clients)
				idle := time.Since(e.lastAccess)
				stalled := numClients > 0 && time.Since(lastData) > 15*time.Second
				e.mu.Unlock()

				if numClients == 0 && idle > 30*time.Second {
					log.Printf("[BVN] No active viewers for 30s, stopping idle worker")
					return
				}
				if stalled {
					log.Printf("[BVN] Worker stalled (no data for 15s), killing and restarting...")
					return
				}
			}
		}
	}()
}
