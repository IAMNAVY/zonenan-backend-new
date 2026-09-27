package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// RandomCode 生成 6 位数字验证码(密码学随机)。
func RandomCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "000000"
	}
	return fmt.Sprintf("%06d", n.Int64())
}
