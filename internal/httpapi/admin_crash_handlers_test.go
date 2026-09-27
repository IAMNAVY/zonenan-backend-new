package httpapi

import (
	"math"
	"net/url"
	"strings"
	"testing"
)

func TestParseAdminCrashQuery(t *testing.T) {
	tests := []struct {
		name      string
		values    url.Values
		page      int64
		search    string
		pattern   string
		numeric   int64
		errorType string
		wantErr   bool
	}{
		{name: "defaults", page: 1, pattern: "%%"},
		{name: "page and ID", values: url.Values{"page": {"2"}, "q": {" 42 "}}, page: 2, search: "42", pattern: "%42%", numeric: 42},
		{name: "message", values: url.Values{"q": {"RangeError"}}, page: 1, search: "RangeError", pattern: "%RangeError%"},
		{name: "category", values: url.Values{"error_type": {" flutter "}}, page: 1, pattern: "%%", errorType: "flutter"},
		{name: "literal wildcards", values: url.Values{"q": {`a%_\b`}}, page: 1, search: `a%_\b`, pattern: `%a\%\_\\b%`},
		{name: "overflow ID is text", values: url.Values{"q": {"9223372036854775808"}}, page: 1, search: "9223372036854775808", pattern: "%9223372036854775808%"},
		{name: "zero page", values: url.Values{"page": {"0"}}, wantErr: true},
		{name: "invalid page", values: url.Values{"page": {"abc"}}, wantErr: true},
		{name: "long search", values: url.Values{"q": {strings.Repeat("字", 201)}}, wantErr: true},
		{name: "long category", values: url.Values{"error_type": {strings.Repeat("字", 101)}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAdminCrashQuery(tt.values)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.page != tt.page || got.search != tt.search || got.pattern != tt.pattern || got.numeric != tt.numeric || got.errorType != tt.errorType {
				t.Fatalf("query = %#v", got)
			}
		})
	}
}

func TestAdminCrashPage(t *testing.T) {
	tests := []struct {
		page, total, want int64
	}{
		{1, 0, 1}, {9, 0, 1}, {2, 25, 1}, {2, 26, 2},
		{3, 50, 2}, {3, 51, 3}, {math.MaxInt64, 26, 2},
	}
	for _, tt := range tests {
		if got := adminCrashPage(tt.page, tt.total); got != tt.want {
			t.Fatalf("adminCrashPage(%d, %d) = %d, want %d", tt.page, tt.total, got, tt.want)
		}
	}
}
