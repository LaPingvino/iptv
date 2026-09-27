package main

import (
	"context"
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

// PiP feeds a live picture-in-picture (with sound) into the Now TV renderer.
//
// The renderer reads raw yuv420p video and raw s16le audio from two pipes.
// Raw data carries no timestamps, so switching sources can never produce the
// timestamp jumps that break overlay/audio sync. A clock-driven writer emits
// exactly pipFPS frames and the matching audio per second, taking data from
// the active source and padding with black/silence when it has none.
//
// Each source is a small transcoder ffmpeg reading the bridge's own channel
// URL (so the normal fallback chain applies) at the lowest available quality.
// The next page's source is started ahead of time ("warm") so the switch on a
// page change is instant.

const (
	pipW, pipH   = 384, 216
	pipFPS       = 25
	pipRate      = 48000
	pipFrameSize = pipW * pipH * 3 / 2
	pipAudioTick = pipRate / pipFPS * 4 // s16le stereo bytes per video frame
)

type pipSource struct {
	path   string
	cancel context.CancelFunc

	mu     sync.Mutex
	frames [][]byte
	audio  []byte
}

func (s *pipSource) stop() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

// trim keeps only fresh data while a source is warming up.
func (s *pipSource) trim(maxFrames, maxAudio int) {
	s.mu.Lock()
	if len(s.frames) > maxFrames {
		s.frames = s.frames[len(s.frames)-maxFrames:]
	}
	if len(s.audio) > maxAudio {
		s.audio = s.audio[len(s.audio)-maxAudio:]
	}
	s.mu.Unlock()
}

type PiP struct {
	mu     sync.Mutex
	active *pipSource
	warm   *pipSource

	videoR, videoW *os.File
	audioR, audioW *os.File
}

// Open creates the pipes and starts the clock-driven writer until ctx ends.
// ReaderFiles must be passed to the renderer as ExtraFiles (fd 3 = video, 4 = audio).
func (p *PiP) Open(ctx context.Context) error {
	var err error
	if p.videoR, p.videoW, err = os.Pipe(); err != nil {
		return err
	}
	if p.audioR, p.audioW, err = os.Pipe(); err != nil {
		return err
	}
	go p.writer(ctx)
	go func() {
		<-ctx.Done()
		p.mu.Lock()
		p.active.stop()
		p.warm.stop()
		p.active, p.warm = nil, nil
		p.mu.Unlock()
		p.videoW.Close()
		p.audioW.Close()
		p.videoR.Close()
		p.audioR.Close()
	}()
	return nil
}

func (p *PiP) ReaderFiles() []*os.File { return []*os.File{p.videoR, p.audioR} }

// Show makes path the active insert and warms up next. Empty paths mean none.
func (p *PiP) Show(ctx context.Context, path, next string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active == nil || p.active.path != path {
		old := p.active
		if p.warm != nil && p.warm.path == path {
			p.active, p.warm = p.warm, nil // already running: instant switch
		} else {
			p.active = p.startSource(ctx, path)
		}
		old.stop()
	}
	if next == "" || next == path {
		p.warm.stop()
		p.warm = nil
	} else if p.warm == nil || p.warm.path != next {
		p.warm.stop()
		p.warm = p.startSource(ctx, next)
	}
}

// tick runs fn at exactly pipFPS per second (catching up after a blocked write)
// until ctx ends or fn returns false.
func tick(ctx context.Context, fn func() bool) {
	start := time.Now()
	for k := 0; ; k++ {
		if d := time.Until(start.Add(time.Duration(k) * time.Second / pipFPS)); d > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		} else if ctx.Err() != nil {
			return
		}
		if !fn() {
			return
		}
	}
}

// Video and audio have their own writers: ffmpeg opens its inputs one at a
// time, so a single writer blocked on one pipe would starve the other.
func (p *PiP) writer(ctx context.Context) {
	go p.videoWriter(ctx)
	go p.audioWriter(ctx)
}

func (p *PiP) current() *pipSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w := p.warm; w != nil {
		w.trim(pipFPS, pipAudioTick*pipFPS) // keep ~1s ready so the switch has sound at once
	}
	return p.active
}

func (p *PiP) videoWriter(ctx context.Context) {
	black := make([]byte, pipFrameSize)
	for i := pipW * pipH; i < len(black); i++ {
		black[i] = 128 // neutral chroma
	}
	last := black
	tick(ctx, func() bool {
		frame := black
		if src := p.current(); src != nil {
			frame = last
			src.mu.Lock()
			if len(src.frames) > 3*pipFPS { // >3s behind: skip ahead to ~1s
				src.frames = src.frames[len(src.frames)-pipFPS:]
			}
			if len(src.frames) > 0 {
				frame, src.frames = src.frames[0], src.frames[1:]
			}
			src.mu.Unlock()
		}
		last = frame
		_, err := p.videoW.Write(frame)
		return err == nil
	})
}

func (p *PiP) audioWriter(ctx context.Context) {
	silence := make([]byte, pipAudioTick)
	tick(ctx, func() bool {
		chunk := silence
		if src := p.current(); src != nil {
			src.mu.Lock()
			if len(src.audio) > pipAudioTick*3*pipFPS { // >3s behind: skip ahead to ~1s
				src.audio = src.audio[len(src.audio)-pipAudioTick*pipFPS:]
			}
			if len(src.audio) >= pipAudioTick {
				chunk = append([]byte(nil), src.audio[:pipAudioTick]...)
				src.audio = src.audio[pipAudioTick:]
			}
			src.mu.Unlock()
		}
		_, err := p.audioW.Write(chunk)
		return err == nil
	})
}

func (p *PiP) startSource(parent context.Context, path string) *pipSource {
	if path == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	s := &pipSource{path: path, cancel: cancel}
	go s.run(ctx)
	return s
}

// run keeps a transcoder going for the source until ctx ends, restarting it
// if the upstream drops.
func (s *pipSource) run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.runOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[PiP] %s: %v", s.path, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *pipSource) runOnce(ctx context.Context) error {
	src := localBridgeURL(s.path)
	prog := lowestVariantProgram(ctx, src)
	vR, vW, err := os.Pipe()
	if err != nil {
		return err
	}
	aR, aW, err := os.Pipe()
	if err != nil {
		vR.Close()
		vW.Close()
		return err
	}
	// -re reads at native speed, smoothing the per-segment bursts of live HLS.
	args := []string{"-nostdin", "-v", "error", "-re", "-user_agent", "Mozilla/5.0", "-i", src}
	vmap, amap := "0:v:0", "0:a:0?"
	if prog >= 0 {
		vmap, amap = fmt.Sprintf("0:p:%d:v:0", prog), fmt.Sprintf("0:p:%d:a:0?", prog)
	}
	args = append(args,
		"-map", vmap,
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,format=yuv420p", pipW, pipH, pipW, pipH, pipFPS),
		"-f", "rawvideo", "pipe:3",
		"-map", amap,
		"-af", fmt.Sprintf("aresample=%d,aformat=sample_fmts=s16:channel_layouts=stereo", pipRate),
		"-f", "s16le", "pipe:4",
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.ExtraFiles = []*os.File{vW, aW}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		vR.Close()
		vW.Close()
		aR.Close()
		aW.Close()
		return err
	}
	vW.Close() // the child holds the write ends now
	aW.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer vR.Close()
		for {
			buf := make([]byte, pipFrameSize)
			if _, err := io.ReadFull(vR, buf); err != nil {
				return
			}
			s.mu.Lock()
			s.frames = append(s.frames, buf)
			if len(s.frames) > 5*pipFPS {
				s.frames = s.frames[len(s.frames)-5*pipFPS:]
			}
			s.mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		defer aR.Close()
		buf := make([]byte, 16384)
		for {
			n, err := aR.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.audio = append(s.audio, buf[:n]...)
				if len(s.audio) > pipAudioTick*5*pipFPS {
					s.audio = s.audio[len(s.audio)-pipAudioTick*5*pipFPS:]
				}
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	err = cmd.Wait()
	wg.Wait()
	return err
}

// localBridgeURL is the bridge's own URL for a stream path, reachable from this host.
func localBridgeURL(path string) string {
	host, port, err := net.SplitHostPort(ListenAddr)
	if err != nil {
		host, port = "127.0.0.1", strconv.Itoa(Port)
	}
	switch {
	case host == "" || host == "0.0.0.0":
		host = "127.0.0.1"
	case host == "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/" + strings.TrimPrefix(path, "/")
}

var variantBWRe = regexp.MustCompile(`[:,]BANDWIDTH=(\d+)`)

// lowestVariantProgram returns the ffmpeg HLS program id of the lowest-bandwidth
// variant in a master playlist (cheapest to decode for a small insert), or -1
// if the URL is not a master playlist.
func lowestVariantProgram(ctx context.Context, masterURL string) int {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, masterURL, nil)
	if err != nil {
		return -1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	best, bestBW, idx := -1, 0, 0
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		if strings.Contains(line, "audio_only") || !strings.Contains(line, "RESOLUTION=") {
			idx++
			continue
		}
		if m := variantBWRe.FindStringSubmatch(line); m != nil {
			if bw, _ := strconv.Atoi(m[1]); best < 0 || bw < bestBW {
				best, bestBW = idx, bw
			}
		}
		idx++
	}
	return best
}
