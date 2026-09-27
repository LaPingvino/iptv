package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FanoutEngine runs one on-demand ffmpeg process that writes MPEG-TS to
// stdout and fans the bytes out to every connected viewer. It starts on the
// first viewer, keeps a short burst buffer for late joiners, and stops 30s
// after the last viewer leaves or when the output stalls for 15s.
type FanoutEngine struct {
	Name string
	// BuildCmd returns the ffmpeg command for a new run; it must write MPEG-TS to stdout.
	BuildCmd func(ctx context.Context) *exec.Cmd
	// OnStart, if set, runs (holding no locks) before ffmpeg starts, e.g. to seed input files.
	OnStart func(ctx context.Context)

	mu           sync.Mutex
	running      bool
	clients      map[chan []byte]struct{}
	recentChunks [][]byte
	lastAccess   time.Time
}

func (e *FanoutEngine) Subscribe() chan []byte {
	ch := make(chan []byte, 256)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.clients == nil {
		e.clients = make(map[chan []byte]struct{})
	}
	e.lastAccess = time.Now()
	e.clients[ch] = struct{}{}
	if !e.running {
		e.startLocked()
	} else {
		for _, c := range e.recentChunks {
			select {
			case ch <- c:
			default:
			}
		}
	}
	return ch
}

func (e *FanoutEngine) Unsubscribe(ch chan []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.clients[ch]; ok {
		delete(e.clients, ch)
		close(ch)
	}
	e.lastAccess = time.Now()
}

// ServeHTTP streams the engine's output to one viewer.
func (e *FanoutEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	ch := e.Subscribe()
	defer e.Unsubscribe(ch)
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

// startLocked launches ffmpeg; the caller holds e.mu.
func (e *FanoutEngine) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	e.running = true
	e.recentChunks = nil

	if e.OnStart != nil {
		e.OnStart(ctx)
	}
	cmd := e.BuildCmd(ctx)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[%s] Failed to open ffmpeg pipe: %v", e.Name, err)
		e.running = false
		cancel()
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[%s] Failed to start ffmpeg: %v", e.Name, err)
		e.running = false
		cancel()
		return
	}
	log.Printf("[%s] Started ffmpeg (PID %d)", e.Name, cmd.Process.Pid)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[%s PANIC RECOVERED] %v", e.Name, rec)
			}
			stdout.Close()
			if waitErr := cmd.Wait(); waitErr != nil && ctx.Err() == nil {
				log.Printf("[%s] ffmpeg exited: %v", e.Name, waitErr)
			}
			cancel()
			e.mu.Lock()
			e.running = false
			for c := range e.clients {
				close(c)
			}
			e.clients = make(map[chan []byte]struct{})
			e.mu.Unlock()
			log.Printf("[%s] Stopped", e.Name)
		}()

		dataChan := make(chan []byte)
		go func() {
			buf := make([]byte, 65536)
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					select {
					case dataChan <- chunk:
					case <-ctx.Done():
						return
					}
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
			case <-time.After(time.Second):
				e.mu.Lock()
				numClients := len(e.clients)
				idle := time.Since(e.lastAccess)
				e.mu.Unlock()
				if numClients == 0 && idle > 30*time.Second {
					log.Printf("[%s] No viewers for 30s, stopping", e.Name)
					return
				}
				if numClients > 0 && time.Since(lastData) > 15*time.Second {
					log.Printf("[%s] Output stalled for 15s, stopping", e.Name)
					return
				}
			}
		}
	}()
}

// loudnormRestreams are upstream HLS channels re-served as MPEG-TS with the
// video copied and the audio levelled to EBU R128 (-23 LUFS), for channels
// whose source audio is far too quiet. Keyed by bridge path (without .ts).
var loudnormRestreams = map[string]*FanoutEngine{
	// TV5Monde Info measures around -31 LUFS, 7-8 dB under every other channel.
	"tv5monde_info": newLoudnormRestream("TV5Monde Info", "https://ott.tv5monde.com/Content/HLS/Live/channel(info)/index.m3u8"),
}

func newLoudnormRestream(name, upstream string) *FanoutEngine {
	return &FanoutEngine{
		Name: name + " (loudnorm)",
		BuildCmd: func(ctx context.Context) *exec.Cmd {
			prog := strconv.Itoa(bestVariantProgram(ctx, upstream))
			return exec.CommandContext(ctx,
				"ffmpeg", "-nostdin", "-v", "warning",
				"-user_agent", "Mozilla/5.0",
				"-live_start_index", "-3",
				"-i", upstream,
				// highest-bandwidth variant: its video plus its audio rendition
				"-map", "0:p:"+prog+":v:0", "-map", "0:p:"+prog+":a:0",
				"-c:v", "copy",
				"-af", "loudnorm=I=-23:TP=-1.5:LRA=11,aresample=48000",
				"-c:a", "aac", "-b:a", "160k",
				"-mpegts_flags", "resend_headers+initial_discontinuity",
				"-f", "mpegts", "pipe:1",
			)
		},
	}
}

var bandwidthRe = regexp.MustCompile(`[:,]BANDWIDTH=(\d+)`)

// bestVariantProgram returns the index (= ffmpeg HLS program id) of the
// highest-bandwidth #EXT-X-STREAM-INF entry in a master playlist, or 0.
func bestVariantProgram(ctx context.Context, masterURL string) int {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, masterURL, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	best, bestBW, idx := 0, -1, 0
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		if m := bandwidthRe.FindStringSubmatch(line); m != nil {
			if bw, _ := strconv.Atoi(m[1]); bw > bestBW {
				best, bestBW = idx, bw
			}
		}
		idx++
	}
	return best
}
