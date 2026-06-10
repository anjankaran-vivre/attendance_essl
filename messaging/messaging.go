package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type CliqConfig struct {
	Token  string
	HookID string
}

var DefaultCliq *CliqConfig

func Init(token, hookID string) {
	DefaultCliq = &CliqConfig{Token: token, HookID: hookID}
}

type webhookPayload struct {
	Channel string `json:"channel,omitempty"`
	Text    string `json:"text"`
	Email   string `json:"email,omitempty"`
}

func (c *CliqConfig) webhookURL() string {
	return fmt.Sprintf("https://cliq.zoho.in/api/v2/bots/%s/incoming?zapikey=%s", c.HookID, c.Token)
}

func send(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	respBody := new(bytes.Buffer)
	respBody.ReadFrom(resp.Body)
	slog.Info("cliq response", "status", resp.StatusCode, "body", respBody.String())

	if resp.StatusCode >= 300 {
		return fmt.Errorf("cliq returned status %d: %s", resp.StatusCode, respBody.String())
	}
	return nil
}

func (c *CliqConfig) SendToChannel(channel, text string) error {
	payload := webhookPayload{Channel: channel, Text: text}
	slog.Info("sending cliq channel message", "channel", channel)
	return send(c.webhookURL(), payload)
}

func (c *CliqConfig) SendDM(email, text string) error {
	payload := webhookPayload{Email: email, Text: text}
	slog.Info("sending cliq DM", "email", email)
	return send(c.webhookURL(), payload)
}

func BuildDuplicateAlert(userID, firstTime, firstFloor, firstDevice, secondTime, secondFloor, secondDevice, minutesGap string) string {
	return fmt.Sprintf(`*Duplicate Punch Alert*
*User ID:* %s
*First Punch:* %s at %s (%s)
*Second Punch:* %s at %s (%s)
*Gap:* %s minutes`, userID, firstTime, firstFloor, firstDevice, secondTime, secondFloor, secondDevice, minutesGap)
}

func BuildLongBreakAlert(userID, outTime, returnTime, floor, minutesGap string) string {
	return fmt.Sprintf(`*Long Break Alert*
*User ID:* %s
*OUT:* %s
*Return:* %s
*Floor:* %s
*Gap:* %s minutes (exceeded threshold)`, userID, outTime, returnTime, floor, minutesGap)
}
