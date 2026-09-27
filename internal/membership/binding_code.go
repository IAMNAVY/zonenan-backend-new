package membership

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	BindingAlgorithmV1 = 1
	bindingPrefix      = "ZN1"
)

var (
	ErrInvalidBindingCode = errors.New("invalid membership binding code")
	ErrInvalidUserID      = errors.New("invalid membership user id")
	ErrInvalidOffset      = errors.New("invalid membership binding offset")
)

// BindingCode is a public payment-note mapping identifier. It is not an
// authentication credential; the offset only changes its appearance.
type BindingCode struct {
	Version    int
	PublicID   int64
	CheckDigit int
	UserID     int64
}

func GenerateBindingCode(userID, offset int64) (string, error) {
	if userID <= 0 {
		return "", ErrInvalidUserID
	}
	if offset <= 0 || userID > 9223372036854775807-offset {
		return "", ErrInvalidOffset
	}
	publicID := userID + offset
	return fmt.Sprintf("%s-%d-%d", bindingPrefix, publicID, luhnCheckDigit(strconv.FormatInt(publicID, 10))), nil
}

// NormalizeBindingCode accepts the user-entered payment-note form and returns
// the canonical form. It does not need the current offset, so old codes remain
// matchable after a future offset rotation.
func NormalizeBindingCode(raw string) (string, error) {
	var compact strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		compact.WriteRune(r)
	}
	value := compact.String()
	if !strings.HasPrefix(value, bindingPrefix) || len(value) <= len(bindingPrefix)+1 {
		return "", ErrInvalidBindingCode
	}
	digits := value[len(bindingPrefix):]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", ErrInvalidBindingCode
		}
	}
	publicDigits := digits[:len(digits)-1]
	if len(publicDigits) == 0 || len(publicDigits) > 18 {
		return "", ErrInvalidBindingCode
	}
	check, _ := strconv.Atoi(digits[len(digits)-1:])
	if check != luhnCheckDigit(publicDigits) {
		return "", ErrInvalidBindingCode
	}
	if _, err := strconv.ParseInt(publicDigits, 10, 64); err != nil {
		return "", ErrInvalidBindingCode
	}
	return fmt.Sprintf("%s-%s-%d", bindingPrefix, publicDigits, check), nil
}

func ParseBindingCode(raw string, offset int64) (BindingCode, error) {
	if offset <= 0 {
		return BindingCode{}, ErrInvalidOffset
	}
	canonical, err := NormalizeBindingCode(raw)
	if err != nil {
		return BindingCode{}, err
	}
	digits := strings.TrimPrefix(canonical, bindingPrefix+"-")
	parts := strings.SplitN(digits, "-", 2)
	publicID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || publicID <= offset {
		return BindingCode{}, ErrInvalidBindingCode
	}
	check, _ := strconv.Atoi(parts[1])
	return BindingCode{
		Version:    BindingAlgorithmV1,
		PublicID:   publicID,
		CheckDigit: check,
		UserID:     publicID - offset,
	}, nil
}

// luhnCheckDigit returns the check digit to append to an integer string.
func luhnCheckDigit(digits string) int {
	sum := 0
	double := true
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return (10 - sum%10) % 10
}
