package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinearStationInterleaving(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "esperanto_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create mock show segments and bumper segments
	mockFiles := []string{
		"dok_estas_parto_01_0001.ts",
		"dok_estas_parto_01_0002.ts",
		"mazi_0001.ts",
		"mazi_0002.ts",
		"senlime_0001.ts",
		"senlime_0002.ts",
	}

	for _, f := range mockFiles {
		p := filepath.Join(tmpDir, f)
		if err := os.WriteFile(p, []byte("mock"), 0644); err != nil {
			t.Fatalf("failed to write mock file: %v", err)
		}
	}

	station := NewLinearStation(tmpDir, "esperanto", 10.0)
	station.mu.RLock()
	schedule := station.schedule
	station.mu.RUnlock()

	// Verify schedule contains show segments followed by bumper segments
	if len(schedule) == 0 {
		t.Fatal("expected non-empty schedule")
	}

	// Total expected = 2 (mazi) + 2 (bumper) + 2 (senlime) + 2 (bumper) = 8
	if len(schedule) != 8 {
		t.Errorf("expected 8 interleaved segments, got %d: %v", len(schedule), schedule)
	}

	// Verify bumper is interleaved
	hasBumperInterleaved := false
	for i, seg := range schedule {
		if strings.HasPrefix(seg, "dok_estas_parto_01") && i > 0 {
			hasBumperInterleaved = true
			break
		}
	}
	if !hasBumperInterleaved {
		t.Error("expected dok_estas_parto_01 bumper to be interleaved between shows")
	}

	// Test playlist generation
	playlist := station.Playlist("/iptv/test/standby.ts")
	if !strings.Contains(playlist, "#EXTM3U") {
		t.Error("playlist missing #EXTM3U header")
	}
	if !strings.Contains(playlist, "#EXT-X-TARGETDURATION:11") {
		t.Error("playlist missing target duration")
	}
	if !strings.Contains(playlist, "/iptv/esperanto/") {
		t.Error("playlist missing esperanto prefix in segment URLs")
	}
}
