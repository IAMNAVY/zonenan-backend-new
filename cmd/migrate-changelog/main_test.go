package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadChangelogSupportsHistoryObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changelog.json")
	data := `{
		"history": [
			{"version":"v2.2.0","date":"2025-01-02","changes":["修复登录","优化启动"]},
			{"version":"2.1.0+12","changelog":"旧版本说明"}
		]
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := readChangelog(path, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Version != "2.2.0" || entries[0].Changelog != "修复登录\n优化启动" {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Version != "2.1.0" || entries[1].Build != 12 || !entries[1].HasBuild {
		t.Fatalf("expected version suffix build to be parsed: %#v", entries[1])
	}
}

func TestMakePlanAssignsUnusedSyntheticBuilds(t *testing.T) {
	entries := []changelogEntry{
		{Version: "2.1.0", Channel: "stable", Source: 0},
		{Version: "2.2.0", Channel: "stable", Source: 1},
	}
	plan, skipped, err := makePlan(entries, map[int]bool{1: true, 2015: true}, map[string]bool{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(plan) != 2 {
		t.Fatalf("unexpected plan: skipped=%d plan=%#v", skipped, plan)
	}
	// Plan output is newest first, while synthetic builds are assigned in
	// chronological order so both version and build ordering remain sensible.
	if plan[0].Version != "2.2.0" || plan[0].Build != 3 || !plan[0].SyntheticBuild {
		t.Fatalf("unexpected newest plan entry: %#v", plan[0])
	}
	if plan[1].Version != "2.1.0" || plan[1].Build != 2 || !plan[1].SyntheticBuild {
		t.Fatalf("unexpected oldest plan entry: %#v", plan[1])
	}
}

func TestMakePlanSkipsExistingVersionAndChannel(t *testing.T) {
	entries := []changelogEntry{{Version: "2.2.0", Channel: "stable"}}
	plan, skipped, err := makePlan(
		entries,
		map[int]bool{},
		map[string]bool{"2.2.0\x00stable": true},
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 || skipped != 1 {
		t.Fatalf("got plan=%#v skipped=%d", plan, skipped)
	}
}
