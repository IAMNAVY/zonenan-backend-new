package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// resendMailer 通过 Resend HTTP API 发信。
type resendMailer struct{ apiKey, from string }

func (m *resendMailer) Send(ctx context.Context, to, subject, body string) error {
	if m.apiKey == "" {
		return fmt.Errorf("RESEND_API_KEY 未配置")
	}
	if m.from == "" {
		return fmt.Errorf("MAIL_FROM 未配置")
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"from":    m.from,
		"to":      []string{to},
		"subject": subject,
		"text":    body,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("resend %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
