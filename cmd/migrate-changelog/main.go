// Command migrate-changelog imports the legacy Website changelog JSON into
// app_releases as stable historical Release Records.
//
// It is intentionally a backfill tool rather than an HTTP/admin operation:
// legacy entries usually have no APK URL or trustworthy build number, so the
// records are inserted directly after validation. Missing build numbers get
// deterministic synthetic values and are reported to the operator.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"zonenan-backend/internal/db"
	"zonenan-backend/internal/store"
)

type changelogEntry struct {
	Version   string
	Build     int
	HasBuild  bool
	Channel   string
	Changelog string
	CreatedAt time.Time
	Source    int
}

type plannedEntry struct {
	changelogEntry
	SyntheticBuild bool
}

func main() {
	filePath := flag.String("file", "", "legacy changelog JSON file path")
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL (defaults to DATABASE_URL)")
	channel := flag.String("channel", store.ReleaseStable, "default channel for entries without one")
	syntheticStart := flag.Int("synthetic-build-start", 1, "first build number used when legacy entries have no build")
	apply := flag.Bool("apply", false, "insert records; without this flag only a dry run is performed")
	flag.Parse()

	if strings.TrimSpace(*filePath) == "" {
		fatal("-file is required")
	}
	if strings.TrimSpace(*databaseURL) == "" {
		fatal("-database-url or DATABASE_URL is required")
	}
	if !validChannel(*channel) {
		fatal("invalid default channel %q", *channel)
	}
	if *syntheticStart <= 0 {
		fatal("-synthetic-build-start must be positive")
	}

	entries, err := readChangelog(*filePath, *channel)
	if err != nil {
		fatal("read changelog: %v", err)
	}
	if len(entries) == 0 {
		fatal("changelog contains no entries")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, *databaseURL)
	if err != nil {
		fatal("connect database: %v", err)
	}
	defer pool.Close()

	existingBuilds, existingKeys, err := existingReleaseState(ctx, pool)
	if err != nil {
		fatal("read app_releases: %v", err)
	}

	plan, skipped, err := makePlan(entries, existingBuilds, existingKeys, *syntheticStart)
	if err != nil {
		fatal("plan migration: %v", err)
	}

	fmt.Printf("读取 %d 条旧 changelog，%d 条待导入，%d 条已存在跳过。\n", len(entries), len(plan), skipped)
	for _, item := range plan {
		if item.SyntheticBuild {
			fmt.Printf("  [synthetic build] %s+%d channel=%s\n", item.Version, item.Build, item.Channel)
		} else {
			fmt.Printf("  %s+%d channel=%s\n", item.Version, item.Build, item.Channel)
		}
	}

	if !*apply {
		fmt.Println("DRY RUN：未写入数据库。确认无误后追加 -apply 执行导入。")
		return
	}

	if err := insertPlan(ctx, pool, plan); err != nil {
		fatal("insert releases: %v", err)
	}
	fmt.Printf("已导入 %d 条历史 Release Record。\n", len(plan))
}

func readChangelog(path, defaultChannel string) ([]changelogEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var rawEntries []map[string]json.RawMessage
	var array []map[string]json.RawMessage
	if err := json.Unmarshal(data, &array); err == nil {
		rawEntries = array
	} else {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, fmt.Errorf("JSON 格式无效: %w", err)
		}
		for _, key := range []string{"history", "changelog", "releases", "items", "data"} {
			raw, ok := object[key]
			if !ok {
				continue
			}
			if err := json.Unmarshal(raw, &rawEntries); err == nil {
				break
			}
		}
		if rawEntries == nil {
			return nil, errors.New("未找到 history/changelog/releases/items/data 数组")
		}
	}

	entries := make([]changelogEntry, 0, len(rawEntries))
	for i, raw := range rawEntries {
		version := normalizeVersion(firstString(raw, "version", "version_name", "tag"))
		if _, _, _, err := store.ParseVersion(version); err != nil {
			return nil, fmt.Errorf("第 %d 条 version 无效 %q: %w", i+1, version, err)
		}

		itemChannel := firstString(raw, "channel")
		if itemChannel == "" {
			itemChannel = defaultChannel
		}
		if !validChannel(itemChannel) {
			return nil, fmt.Errorf("第 %d 条 channel 无效 %q", i+1, itemChannel)
		}

		build, hasBuild := firstInt(raw, "build_number", "build", "version_code", "versionCode")
		if !hasBuild {
			if fromVersion, ok := versionBuild(rawString(raw, "version")); ok {
				build, hasBuild = fromVersion, true
			}
		}
		if hasBuild && build <= 0 {
			return nil, fmt.Errorf("第 %d 条 build 必须为正整数", i+1)
		}

		createdAt := time.Now().UTC()
		if value := firstString(raw, "created_at", "published_at", "date", "published"); value != "" {
			parsed, err := parseDate(value)
			if err != nil {
				return nil, fmt.Errorf("第 %d 条日期无效 %q: %w", i+1, value, err)
			}
			createdAt = parsed
		}

		entries = append(entries, changelogEntry{
			Version:   version,
			Build:     build,
			HasBuild:  hasBuild,
			Channel:   itemChannel,
			Changelog: firstChangelog(raw),
			CreatedAt: createdAt,
			Source:    i,
		})
	}
	return entries, nil
}

func makePlan(entries []changelogEntry, existingBuilds map[int]bool, existingKeys map[string]bool, syntheticStart int) ([]plannedEntry, int, error) {
	used := make(map[int]bool, len(existingBuilds)+len(entries))
	for build := range existingBuilds {
		used[build] = true
	}

	pending := make([]changelogEntry, 0, len(entries))
	plan := make([]plannedEntry, 0, len(entries))
	skipped := 0
	seenKeys := make(map[string]bool)
	for _, entry := range entries {
		key := entry.Version + "\x00" + entry.Channel
		if existingKeys[key] || seenKeys[key] {
			skipped++
			continue
		}
		seenKeys[key] = true
		if entry.HasBuild {
			if used[entry.Build] {
				return nil, 0, fmt.Errorf("%s+%d 已存在或在输入中重复", entry.Version, entry.Build)
			}
			used[entry.Build] = true
			plan = append(plan, plannedEntry{changelogEntry: entry})
		} else {
			pending = append(pending, entry)
		}
	}

	// Assign synthetic builds in chronological order. They are only a database
	// ordering key for legacy changelog rows; they are not claimed to be the APK
	// versionCode of the old package.
	sort.SliceStable(pending, func(i, j int) bool {
		leftMajor, leftMinor, leftPatch, _ := store.ParseVersion(pending[i].Version)
		rightMajor, rightMinor, rightPatch, _ := store.ParseVersion(pending[j].Version)
		if leftMajor != rightMajor {
			return leftMajor < rightMajor
		}
		if leftMinor != rightMinor {
			return leftMinor < rightMinor
		}
		if leftPatch != rightPatch {
			return leftPatch < rightPatch
		}
		if !pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].CreatedAt.Before(pending[j].CreatedAt)
		}
		return pending[i].Source < pending[j].Source
	})

	next := syntheticStart
	for _, entry := range pending {
		for used[next] {
			next++
		}
		if next <= 0 {
			return nil, 0, errors.New("synthetic build number overflow")
		}
		entry.Build = next
		entry.HasBuild = true
		used[next] = true
		plan = append(plan, plannedEntry{changelogEntry: entry, SyntheticBuild: true})
		next++
	}

	// Keep output deterministic and easy to review.
	sort.SliceStable(plan, func(i, j int) bool {
		if plan[i].Version != plan[j].Version {
			leftMajor, leftMinor, leftPatch, _ := store.ParseVersion(plan[i].Version)
			rightMajor, rightMinor, rightPatch, _ := store.ParseVersion(plan[j].Version)
			if leftMajor != rightMajor {
				return leftMajor > rightMajor
			}
			if leftMinor != rightMinor {
				return leftMinor > rightMinor
			}
			return leftPatch > rightPatch
		}
		return plan[i].Build > plan[j].Build
	})
	return plan, skipped, nil
}

func existingReleaseState(ctx context.Context, pool *db.Pool) (map[int]bool, map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version, channel, build_number FROM app_releases`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	builds := map[int]bool{}
	keys := map[string]bool{}
	for rows.Next() {
		var version, channel string
		var build int
		if err := rows.Scan(&version, &channel, &build); err != nil {
			return nil, nil, err
		}
		builds[build] = true
		keys[version+"\x00"+channel] = true
	}
	return builds, keys, rows.Err()
}

func insertPlan(ctx context.Context, pool *db.Pool, plan []plannedEntry) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, entry := range plan {
		major, minor, patch, err := store.ParseVersion(entry.Version)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO app_releases(
				version, version_major, version_minor, version_patch, build_number,
				channel, changelog, active, created_at, updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,TRUE,$8,$8)`,
			entry.Version, major, minor, patch, entry.Build, entry.Channel,
			entry.Changelog, entry.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert %s+%d: %w", entry.Version, entry.Build, err)
		}
	}
	return tx.Commit(ctx)
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	if plus := strings.IndexByte(value, '+'); plus >= 0 {
		value = value[:plus]
	}
	return value
}

func versionBuild(value string) (int, bool) {
	value = strings.TrimSpace(value)
	plus := strings.LastIndexByte(value, '+')
	if plus < 0 {
		return 0, false
	}
	build, err := strconv.Atoi(strings.TrimSpace(value[plus+1:]))
	return build, err == nil && build > 0
}

func firstString(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := rawString(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func rawString(raw map[string]json.RawMessage, key string) string {
	value, ok := raw[key]
	if !ok {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

func firstInt(raw map[string]json.RawMessage, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		var number json.Number
		if json.Unmarshal(value, &number) == nil {
			parsed, err := strconv.Atoi(string(number))
			if err == nil {
				return parsed, true
			}
		}
		if text := rawString(raw, key); text != "" {
			parsed, err := strconv.Atoi(text)
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func firstChangelog(raw map[string]json.RawMessage) string {
	if text := firstString(raw, "changelog", "description", "content", "notes"); text != "" {
		return text
	}
	for _, key := range []string{"changes", "items", "features"} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		var stringsOnly []string
		if json.Unmarshal(value, &stringsOnly) == nil {
			return strings.Join(stringsOnly, "\n")
		}
	}
	return ""
}

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"2006-01-02",
		"2006/01/02",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("支持 RFC3339 或 YYYY-MM-DD 格式")
}

func validChannel(channel string) bool {
	switch channel {
	case store.ReleaseStable, store.ReleaseBeta, store.ReleaseRC, store.ReleaseInternal:
		return true
	default:
		return false
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migrate-changelog: "+format+"\n", args...)
	os.Exit(1)
}
