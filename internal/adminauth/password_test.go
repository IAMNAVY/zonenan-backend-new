package adminauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPasswordPolicyRejectsBeforeDatabase(t *testing.T) {
	for _, next := range []string{"", "short", strings.Repeat("x", 73), strings.Repeat("字", 25), "unchanged-password"} {
		err := (&Store{}).ChangePassword(context.Background(), 1, "unchanged-password", next)
		if !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("length %d: %v", len(next), err)
		}
	}
}
