package afdian

import (
	"bytes"
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL  = "https://afdian.com"
	maxResponseBody = 4 << 20
)

var ErrRemote = errors.New("afdian api request failed")

type Client struct {
	baseURL string
	userID  string
	token   string
	http    *http.Client
}

func NewClient(baseURL, userID, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		userID:  strings.TrimSpace(userID),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

type Order struct {
	OutTradeNo     string          `json:"out_trade_no"`
	CustomOrderID  string          `json:"custom_order_id"`
	UserID         string          `json:"user_id"`
	UserPrivateID  string          `json:"user_private_id"`
	PlanID         string          `json:"plan_id"`
	Title          string          `json:"title"`
	Month          int             `json:"month"`
	TotalAmount    string          `json:"total_amount"`
	ShowAmount     string          `json:"show_amount"`
	Status         int             `json:"status"`
	Remark         string          `json:"remark"`
	RedeemID       string          `json:"redeem_id"`
	ProductType    int             `json:"product_type"`
	Discount       string          `json:"discount"`
	SKUDetail      json.RawMessage `json:"sku_detail"`
	AddressPerson  string          `json:"address_person"`
	AddressPhone   string          `json:"address_phone"`
	AddressAddress string          `json:"address_address"`
}

type Webhook struct {
	EC   int    `json:"ec"`
	EM   string `json:"em"`
	Data struct {
		Type  string `json:"type"`
		Order Order  `json:"order"`
		Sign  string `json:"sign"`
	} `json:"data"`
}

func VerifyWebhookSignature(order Order, signature, publicKeyPEM string) error {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return errors.New("invalid afdian webhook public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse afdian webhook public key: %w", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return errors.New("afdian webhook public key is not RSA")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("decode afdian webhook signature: %w", err)
	}
	if len(decoded) == 0 {
		return errors.New("afdian webhook signature is empty")
	}
	digest := sha256.Sum256([]byte(webhookSignString(order)))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], decoded); err != nil {
		return fmt.Errorf("verify afdian webhook signature: %w", err)
	}
	return nil
}

func webhookSignString(order Order) string {
	return order.OutTradeNo + order.UserID + order.PlanID + order.TotalAmount
}

type orderResponse struct {
	EC   int    `json:"ec"`
	EM   string `json:"em"`
	Data struct {
		List       []Order `json:"list"`
		TotalCount int     `json:"total_count"`
		TotalPage  int     `json:"total_page"`
	} `json:"data"`
}

func Sign(token, params string, ts int64, userID string) string {
	value := token + "params" + params + "ts" + strconv.FormatInt(ts, 10) + "user_id" + userID
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (c *Client) QueryOrders(ctx context.Context, page int) (orders []Order, totalPage int, err error) {
	if page <= 0 {
		page = 1
	}
	var response orderResponse
	if err := c.post(ctx, "/api/open/query-order", map[string]any{"page": page}, &response); err != nil {
		return nil, 0, err
	}
	return response.Data.List, response.Data.TotalPage, nil
}

func (c *Client) Ping(ctx context.Context) error {
	var response map[string]any
	return c.post(ctx, "/api/open/ping", map[string]any{"a": 333}, &response)
}

func (c *Client) post(ctx context.Context, path string, params any, out any) error {
	if c.userID == "" || c.token == "" {
		return fmt.Errorf("%w: api credentials are not configured", ErrRemote)
	}
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}
	paramsString := string(paramsBytes)
	ts := time.Now().Unix()
	body := map[string]any{
		"user_id": c.userID,
		"params":  paramsString,
		"ts":      ts,
		"sign":    Sign(c.token, paramsString, ts, c.userID),
	}
	requestBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(requestBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRemote, err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseBody)
	responseBytes, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: http %d", ErrRemote, resp.StatusCode)
	}
	var envelope struct {
		EC int    `json:"ec"`
		EM string `json:"em"`
	}
	if err := json.Unmarshal(responseBytes, &envelope); err != nil {
		return fmt.Errorf("%w: invalid json response", ErrRemote)
	}
	if envelope.EC != 200 {
		return fmt.Errorf("%w: ec=%d em=%s", ErrRemote, envelope.EC, sanitizeMessage(envelope.EM))
	}
	if err := json.Unmarshal(responseBytes, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func sanitizeMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 200 {
		return value[:200]
	}
	return value
}

// EncodeForm is kept for compatibility with deployments that use form requests
// while the current client sends JSON, as both are accepted by Afdian.
func EncodeForm(values url.Values) string { return values.Encode() }
