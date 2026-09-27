package httpapi

import (
	"math"
	"net/url"
	"strings"
	"testing"
)

func TestParseAdminUserQuery(t *testing.T) {
	tests := []struct {
		name    string
		values  url.Values
		page    int64
		search  string
		pattern string
		userID  int64
		wantErr bool
	}{
		{name: "defaults", page: 1, pattern: "%%"},
		{name: "page and trimmed ID", values: url.Values{"page": {"2"}, "q": {" 42 "}}, page: 2, search: "42", pattern: "%42%", userID: 42},
		{name: "nickname", values: url.Values{"q": {" 测试用户 "}}, page: 1, search: "测试用户", pattern: "%测试用户%"},
		{name: "identity", values: url.Values{"q": {"email:User@example.com"}}, page: 1, search: "email:User@example.com", pattern: "%email:User@example.com%"},
		{name: "literal wildcards", values: url.Values{"q": {`a%_\b`}}, page: 1, search: `a%_\b`, pattern: `%a\%\_\\b%`},
		{name: "blank search", values: url.Values{"q": {" \t "}}, page: 1, pattern: "%%"},
		{name: "max page", values: url.Values{"page": {"9223372036854775807"}}, page: math.MaxInt64, pattern: "%%"},
		{name: "overflow ID is text", values: url.Values{"q": {"9223372036854775808"}}, page: 1, search: "9223372036854775808", pattern: "%9223372036854775808%"},
		{name: "fixed page size", values: url.Values{"page_size": {"1000"}}, page: 1, pattern: "%%"},
		{name: "zero page", values: url.Values{"page": {"0"}}, wantErr: true},
		{name: "negative page", values: url.Values{"page": {"-1"}}, wantErr: true},
		{name: "noninteger page", values: url.Values{"page": {"1.5"}}, wantErr: true},
		{name: "invalid page", values: url.Values{"page": {"abc"}}, wantErr: true},
		{name: "overflow page", values: url.Values{"page": {"9223372036854775808"}}, wantErr: true},
		{name: "long search", values: url.Values{"q": {strings.Repeat("字", 201)}}, wantErr: true},
		{name: "search length counts characters", values: url.Values{"q": {strings.Repeat("字", 200)}}, page: 1, search: strings.Repeat("字", 200), pattern: "%" + strings.Repeat("字", 200) + "%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAdminUserQuery(tt.values)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.page != tt.page || got.search != tt.search || got.pattern != tt.pattern || got.userID != tt.userID {
				t.Fatalf("query = %#v, want page=%d search=%q pattern=%q userID=%d", got, tt.page, tt.search, tt.pattern, tt.userID)
			}
		})
	}
}

func TestAdminUserPage(t *testing.T) {
	tests := []struct {
		name              string
		page, total, want int64
	}{
		{"empty", 1, 0, 1},
		{"empty past end", 9, 0, 1},
		{"first page", 1, 26, 1},
		{"full page", 2, 25, 1},
		{"second page", 2, 26, 2},
		{"two full pages", 3, 50, 2},
		{"partial third page", 3, 51, 3},
		{"deleted last user", 2, 25, 1},
		{"overflow-safe page", math.MaxInt64, 26, 2},
		{"overflow-safe total", math.MaxInt64, math.MaxInt64, (math.MaxInt64-1)/25 + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminUserPage(tt.page, tt.total); got != tt.want {
				t.Fatalf("page = %d, want %d", got, tt.want)
			}
		})
	}
}
