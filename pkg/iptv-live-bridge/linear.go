package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// LinearStation manages 24/7 sliding-window HLS playlists with bumper interleaving.
type LinearStation struct {
	dir         string
	prefix      string
	segDuration float64
	mu          sync.RWMutex
	lastScan    time.Time
	schedule    []string
}

func NewLinearStation(dir, prefix string, segDuration float64) *LinearStation {
	ls := &LinearStation{
		dir:         dir,
		prefix:      strings.Trim(prefix, "/"),
		segDuration: segDuration,
	}
	ls.rebuildSchedule()
	return ls
}

func (ls *LinearStation) rebuildSchedule() {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	entries, err := os.ReadDir(ls.dir)
	if err != nil || len(entries) == 0 {
		ls.schedule = nil
		return
	}

	var allSegs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			allSegs = append(allSegs, e.Name())
		}
	}
	sort.Strings(allSegs)

	// If this is Esperanto TV, perform intelligent episode interleaving with the Esperanto Estas intro bumper!
	if strings.Contains(ls.prefix, "esperanto") {
		var bumperSegs []string
		shows := make(map[string][]string)

		for _, seg := range allSegs {
			if strings.HasPrefix(seg, "dok_estas_parto_01_") {
				bumperSegs = append(bumperSegs, seg)
			} else {
				// Group by show/episode prefix (everything up to the last underscore)
				idx := strings.LastIndex(seg, "_")
				var key string
				if idx != -1 {
					key = seg[:idx]
				} else {
					key = "misc"
				}
				shows[key] = append(shows[key], seg)
			}
		}

		if len(bumperSegs) > 0 && len(shows) > 0 {
			var showKeys []string
			for k := range shows {
				showKeys = append(showKeys, k)
			}
			sort.Strings(showKeys)

			var interleaved []string
			for _, k := range showKeys {
				// 1. Add the show episode segments
				interleaved = append(interleaved, shows[k]...)
				// 2. Add the Esperanto Estas introduction as the station bumper!
				interleaved = append(interleaved, bumperSegs...)
			}
			ls.schedule = interleaved
			ls.lastScan = time.Now()
			return
		}
	}

	// Default linear sequencing
	ls.schedule = allSegs
	ls.lastScan = time.Now()
}

func (ls *LinearStation) Playlist(standbyTS string) string {
	ls.mu.RLock()
	if time.Since(ls.lastScan) > 10*time.Minute || len(ls.schedule) == 0 {
		ls.mu.RUnlock()
		ls.rebuildSchedule()
		ls.mu.RLock()
	}
	defer ls.mu.RUnlock()

	targetDur := int(ls.segDuration) + 1
	if len(ls.schedule) == 0 {
		return fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:%.6f,\n%s\n#EXT-X-ENDLIST\n",
			targetDur, ls.segDuration, standbyTS)
	}

	totalSegs := len(ls.schedule)
	totalCycleTime := float64(totalSegs) * ls.segDuration

	now := float64(time.Now().UnixNano()) / 1e9
	currentOffset := float64(int64(now*1000)%(int64(totalCycleTime*1000))) / 1000.0
	currentIdx := int(currentOffset / ls.segDuration)
	mediaSequence := int(now / ls.segDuration)

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:3\n")
	fmt.Fprintf(&sb, "#EXT-X-TARGETDURATION:%d\n", targetDur)
	fmt.Fprintf(&sb, "#EXT-X-MEDIA-SEQUENCE:%d\n", mediaSequence)

	for k := 0; k < 5; k++ {
		segIdx := (currentIdx + k) % totalSegs
		segName := ls.schedule[segIdx]
		fmt.Fprintf(&sb, "#EXTINF:%.6f,\n", ls.segDuration)
		fmt.Fprintf(&sb, "/iptv/%s/%s\n", ls.prefix, segName)
	}

	return sb.String()
}
