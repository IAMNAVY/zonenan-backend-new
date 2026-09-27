package afdian

import (
	"crypto"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestSignUsesRawParams(t *testing.T) {
	got := Sign("token", `{"page":1}`, 1624339905, "user")
	if got == "" || len(got) != 32 {
		t.Fatalf("unexpected md5 signature: %q", got)
	}
	if got == Sign("token", `{"page": 1}`, 1624339905, "user") {
		t.Fatal("signature ignored raw params formatting")
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER})
	order := Order{
		OutTradeNo:  "order-123",
		UserID:      "afdian-user",
		PlanID:      "plan-456",
		TotalAmount: "5.00",
	}
	digest := sha256.Sum256([]byte(webhookSignString(order)))
	signature, err := rsa.SignPKCS1v15(cryptorand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign order: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(signature)

	if err := VerifyWebhookSignature(order, encoded, string(publicKeyPEM)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	changed := order
	changed.TotalAmount = "50.00"
	if err := VerifyWebhookSignature(changed, encoded, string(publicKeyPEM)); err == nil {
		t.Fatal("signature accepted after signed order field changed")
	}
	for _, value := range []string{"", "not-base64"} {
		if err := VerifyWebhookSignature(order, value, string(publicKeyPEM)); err == nil {
			t.Fatalf("invalid signature %q was accepted", value)
		}
	}
}

func TestWebhookDecodesSignatureAndKnownOrderFields(t *testing.T) {
	var payload Webhook
	body := `{"ec":200,"data":{"type":"order","sign":"signature","order":{"out_trade_no":"order-123","custom_order_id":"custom-1","user_id":"afdian-user","plan_id":"plan-456","title":"Premium","total_amount":"5.00","address_person":"A"}}}`
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&payload); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}
	if payload.Data.Sign != "signature" || payload.Data.Order.CustomOrderID != "custom-1" || payload.Data.Order.Title != "Premium" || payload.Data.Order.AddressPerson != "A" {
		t.Fatalf("webhook fields were not decoded: %+v", payload)
	}
}
