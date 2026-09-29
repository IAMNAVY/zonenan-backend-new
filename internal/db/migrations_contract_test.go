package db

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestLegacyMigrationManifest(t *testing.T) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			got = append(got, entry.Name())
		}
	}
	sort.Strings(got)

	want := []string{
		"0001_init.sql", "0002_grades.sql", "0003_app_settings.sql", "0004_legal_docs.sql",
		"0005_grade_favorites.sql", "0005_legal_seed.sql", "0006_free_query_limit.sql",
		"0007_passkey.sql", "0008_grade_config_seed.sql", "0009_user_role.sql",
		"0010_free_query_dedup.sql", "0011_trusted_devices.sql", "0012_announcements.sql",
		"0013_home_ads.sql", "0014_telemetry_risk.sql", "0015_app_settings_seed.sql",
		"0016_email_code_attempts.sql", "0017_analytics.sql", "0018_analytics_privacy.sql",
		"0019_analytics_user_versions.sql", "0020_analytics_shared_installations.sql",
		"0021_device_login_challenges.sql", "0021_user_last_online.sql", "0022_content_audience.sql",
		"0023_analytics_device_metadata.sql", "0024_analytics_widgets.sql", "0024_app_releases.sql",
		"0025_release_apk_metadata.sql", "0026_memberships.sql", "0027_memberships_fixes.sql",
		"0028_membership_entitlements.sql", "0029_membership_widget_entitlement.sql",
		"0030_release_update_mode.sql", "0031_classroom_data_artifacts.sql",
		"0032_release_whats_new.sql", "0033_campus_map_places.sql", "0034_campus_map_revision.sql",
		"0035_campus_map_categories.sql", "0036_refine_campus_map_types.sql",
		"0037_campus_map_contributions.sql", "0038_campus_map_contribution_quality.sql",
		"0039_post_upgrade_guide_feature.sql", "0040_analytics_map_calendar.sql",
		"0041_campus_map_entrance_type.sql", "0042_campus_map_incidents.sql", "0043_web_sessions.sql",
		"0044_campus_map_charging_station.sql", "0045_admin_auth_rbac_audit.sql",
		"0046_merchant_platform.sql",
		"0047_poi_rentals.sql",
		"0048_jobs_and_aggregates.sql",
		"0049_opening_hours.sql",
		"0050_rental_owner_fk.sql",
		"0051_job_idempotency.sql",
		"0052_push_notifications.sql",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy migration manifest changed\ngot:  %#v\nwant: %#v", got, want)
	}
}
