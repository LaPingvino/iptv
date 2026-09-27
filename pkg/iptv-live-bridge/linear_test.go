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

	// Create mock show segments and the station ident (bumper)
	mockFiles := []string{
		"stacia_vineto_0000.ts",
		"dok_estas_parto_01_0001.ts",
		"dok_estas_parto_01_0002.ts",
		"pasporto_01_0001.ts",
		"pasporto_01_0002.ts",
		"senlime_s01e01_0001.ts",
		"senlime_s01e01_0002.ts",
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

	// pasporto (2) + ident (1) + senlime (2) + ident (1) + Esperanto Estas doc (2) + ident (1) = 9
	if len(schedule) != 9 {
		t.Errorf("expected 9 interleaved segments, got %d", len(schedule))
	}

	// Verify bumper is interleaved
	hasBumperInterleaved := false
	for i, seg := range schedule {
		if strings.HasPrefix(seg.Name, "stacia_vineto_") && i > 0 {
			hasBumperInterleaved = true
			break
		}
	}
	if !hasBumperInterleaved {
		t.Error("expected stacia_vineto ident to be interleaved between shows")
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
