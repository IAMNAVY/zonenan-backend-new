package store

import (
	"strings"
	"testing"
)

func TestValidateReleaseWhatsNew(t *testing.T) {
	base := ReleaseWhatsNew{
		Title: "课表通知来了",
		Items: []ReleaseWhatsNewItem{{
			Title: "课前提醒", Body: "开课前收到提醒", Platforms: []string{"android"},
			ActionID: "open_reminder_settings", ActionLabel: "去设置", ImageURL: "https://example.test/guide.png",
		}},
	}
	if err := ValidateReleaseWhatsNew(nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseWhatsNew(&base); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ReleaseWhatsNew){
		"no title":         func(g *ReleaseWhatsNew) { g.Title = "" },
		"no items":         func(g *ReleaseWhatsNew) { g.Items = nil },
		"too many items":   func(g *ReleaseWhatsNew) { g.Items = append(g.Items, g.Items[0], g.Items[0], g.Items[0]) },
		"large body":       func(g *ReleaseWhatsNew) { g.Items[0].Body = strings.Repeat("字", 601) },
		"http image":       func(g *ReleaseWhatsNew) { g.Items[0].ImageURL = "http://example.test/img.png" },
		"local image":      func(g *ReleaseWhatsNew) { g.Items[0].ImageURL = "https://127.0.0.1/img.png" },
		"invalid image":    func(g *ReleaseWhatsNew) { g.Items[0].ImageURL = "https://%zz" },
		"unknown action":   func(g *ReleaseWhatsNew) { g.Items[0].ActionID = "open_any_url" },
		"wrong platform":   func(g *ReleaseWhatsNew) { g.Items[0].Platforms = []string{"windows"} },
		"duplicate target": func(g *ReleaseWhatsNew) { g.Items[0].Platforms = []string{"android", "android"} },
	} {
		guide := base
		guide.Items = append([]ReleaseWhatsNewItem(nil), base.Items...)
		guide.Items[0].Platforms = append([]string(nil), base.Items[0].Platforms...)
		change(&guide)
		if err := ValidateReleaseWhatsNew(&guide); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
}
