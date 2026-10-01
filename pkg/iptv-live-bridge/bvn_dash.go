package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// BVN DASH fetcher: instead of letting ffmpeg's DASH demuxer follow the live
// manifest (with -re it skipped about half the video segments, without it it
// re-read parts of the timeline), the bridge fetches every segment itself, in
// order, and feeds ffmpeg one continuous fragmented MP4 per track over a pipe.
// ffmpeg then only decrypts (-decryption_key) and remuxes to MPEG-TS.

type dashMPD struct {
	BaseURL []string `xml:"BaseURL"`
	Periods []struct {
		BaseURL        []string `xml:"BaseURL"`
		AdaptationSets []struct {
			ContentType string `xml:"contentType,attr"`
			MimeType    string `xml:"mimeType,attr"`
			Template    struct {
				Timescale      int64  `xml:"timescale,attr"`
				Initialization string `xml:"initialization,attr"`
				Media          string `xml:"media,attr"`
				Timeline       []struct {
					T int64 `xml:"t,attr"`
					D int64 `xml:"d,attr"`
					R int64 `xml:"r,attr"`
				} `xml:"SegmentTimeline>S"`
			} `xml:"SegmentTemplate"`
			Representations []struct {
				ID        string `xml:"id,attr"`
				Bandwidth int64  `xml:"bandwidth,attr"`
			} `xml:"Representation"`
		} `xml:"AdaptationSet"`
	} `xml:"Period"`
}

// dashTrack is one track's view of the latest manifest.
type dashTrack struct {
	base      string // absolute URL prefix for segments
	init      string
	media     string // with $Time$ still in it
	timescale int64
	times     []int64 // segment start times currently listed, ascending
}

func (t dashTrack) url(name string) string { return t.base + name }

// fetchBVNTracks reads the live manifest and returns the audio track and the
// highest-bandwidth video track.
func fetchBVNTracks(ctx context.Context) (audio, video dashTrack, err error) {
	mpdURL, err := getBVNStreamURL(ctx)
	if err != nil {
		return audio, video, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mpdURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return audio, video, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return audio, video, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}
	var m dashMPD
	if err := xml.NewDecoder(resp.Body).Decode(&m); err != nil {
		return audio, video, err
	}
	if len(m.Periods) == 0 {
		return audio, video, fmt.Errorf("manifest has no period")
	}
	base := mpdURL[:strings.LastIndex(mpdURL, "/")+1]
	if len(m.BaseURL) > 0 && strings.HasPrefix(m.BaseURL[0], "http") {
		base = m.BaseURL[0]
	}
	p := m.Periods[0]
	if len(p.BaseURL) > 0 {
		base += p.BaseURL[0]
	}
	var haveA, haveV bool
	for _, as := range p.AdaptationSets {
		kind := as.ContentType
		if kind == "" {
			kind = strings.SplitN(as.MimeType, "/", 2)[0]
		}
		if (kind != "audio" && kind != "video") || len(as.Representations) == 0 {
			continue
		}
		if (kind == "audio" && haveA) || (kind == "video" && haveV) {
			continue
		}
		rep := as.Representations[0]
		for _, r := range as.Representations {
			if r.Bandwidth > rep.Bandwidth {
				rep = r
			}
		}
		tpl := as.Template
		tr := dashTrack{
			base:      base,
			init:      strings.ReplaceAll(tpl.Initialization, "$RepresentationID$", rep.ID),
			media:     strings.ReplaceAll(tpl.Media, "$RepresentationID$", rep.ID),
			timescale: tpl.Timescale,
		}
		var t int64
		for _, s := range tpl.Timeline {
			if s.T != 0 {
				t = s.T
			}
			for i := int64(0); i <= s.R; i++ {
				tr.times = append(tr.times, t)
				t += s.D
			}
		}
		if kind == "audio" {
			audio, haveA = tr, true
		} else {
			video, haveV = tr, true
		}
	}
	if !haveA || !haveV || len(audio.times) == 0 || len(video.times) == 0 {
		return audio, video, fmt.Errorf("manifest lacks audio or video segments")
	}
	return audio, video, nil
}

func httpGetBytes(ctx context.Context, u string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// bvnLiveOffsetSegs: start this many segments behind the newest listed one
// (~6s), so each fetch is of a segment that is fully published.
const bvnLiveOffsetSegs = 3

// feedBVNTrack writes the init segment and then every media segment of one
// track, in order, to w until ctx ends. startSec aligns both tracks on the same
// presentation time. The manifest is re-read every 2s for new segments.
func feedBVNTrack(ctx context.Context, kind string, startSec float64, w io.WriteCloser) {
	defer w.Close()
	pick := func(a, v dashTrack) dashTrack {
		if kind == "audio" {
			return a
		}
		return v
	}
	var next int64 = -1
	initDone := false
	for ctx.Err() == nil {
		a, v, err := fetchBVNTracks(ctx)
		if err != nil {
			log.Printf("[BVN] %s manifest: %v", kind, err)
			if !sleepCtx(ctx, 2*time.Second) {
				return
			}
			continue
		}
		tr := pick(a, v)
		if !initDone {
			b, err := httpGetBytes(ctx, tr.url(tr.init))
			if err != nil {
				log.Printf("[BVN] %s init: %v", kind, err)
				if !sleepCtx(ctx, 2*time.Second) {
					return
				}
				continue
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			initDone = true
		}
		if next < 0 {
			// first segment starting at or after startSec
			target := int64(startSec * float64(tr.timescale))
			next = tr.times[len(tr.times)-1]
			for _, t := range tr.times {
				if t >= target {
					next = t
					break
				}
			}
		}
		if next < tr.times[0] {
			log.Printf("[BVN] %s fell behind the manifest window, jumping ahead", kind)
			next = tr.times[0]
		}
		for _, t := range tr.times {
			if t < next {
				continue
			}
			b, err := httpGetBytes(ctx, tr.url(strings.ReplaceAll(tr.media, "$Time$", strconv.FormatInt(t, 10))))
			if err != nil {
				log.Printf("[BVN] %s segment %d: %v", kind, t, err)
				break // retry from this segment after the next manifest read
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			next = t + 1
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// startBVNFeeds picks a common start time and starts both track feeders,
// returning the read ends ffmpeg should get as fd 3 (video) and fd 4 (audio).
func startBVNFeeds(ctx context.Context) (video, audio *os.File, err error) {
	_, v, err := fetchBVNTracks(ctx)
	if err != nil {
		return nil, nil, err
	}
	i := len(v.times) - 1 - bvnLiveOffsetSegs
	if i < 0 {
		i = 0
	}
	startSec := float64(v.times[i]) / float64(v.timescale)
	vR, vW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	aR, aW, err := os.Pipe()
	if err != nil {
		vR.Close()
		vW.Close()
		return nil, nil, err
	}
	go feedBVNTrack(ctx, "video", startSec, vW)
	// audio segments start slightly before the video one so no sound is lost
	go feedBVNTrack(ctx, "audio", startSec-0.5, aW)
	return vR, aR, nil
}
