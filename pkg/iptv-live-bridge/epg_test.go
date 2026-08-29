package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinearEPGGeneration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "epg_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockFiles := []string{
		"dok_estas_parto_01_0001.ts",
		"mazi_0001.ts",
		"mazi_0002.ts",
	}

	for _, f := range mockFiles {
		p := filepath.Join(tmpDir, f)
		os.WriteFile(p, []byte("mock"), 0644)
	}

	station := NewLinearStation(tmpDir, "esperanto", 10.0)
	mgr := &EPGManager{}
	xml := mgr.GetLinearEPG(station, "EsperantoTV.eo@SD", "Esperanto TV", "eo")

	if !strings.Contains(xml, "<tv source-info-url=") {
		t.Error("missing <tv> root tag in EPG")
	}
	if !strings.Contains(xml, "<channel id=\"EsperantoTV.eo@SD\">") {
		t.Error("missing channel declaration in EPG")
	}
	if !strings.Contains(xml, "<programme start=") {
		t.Error("missing <programme> entries in EPG")
	}
	if !strings.Contains(xml, "Esperanto Estas: Enkonduko") {
		t.Error("missing Esperanto Estas bumper programme entry in EPG")
	}
}

func TestFallbackBaselineEPG(t *testing.T) {
	xml := fallbackBaselineEPG()
	if !strings.Contains(xml, "Speedrun.tv") {
		t.Errorf("unexpected fallback EPG: %s", xml)
	}
	if !strings.Contains(xml, "Live status unknown") {
		t.Errorf("expected 'Live status unknown' baseline in fallback EPG")
	}
}
