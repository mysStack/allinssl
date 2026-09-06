package report

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSlackReportSendText(t *testing.T) {
	const webhookURL = "https://hooks.slack.com/services/T00000000/B00000000/secret-token"

	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{name: "success", statusCode: http.StatusOK},
		{name: "rejects non-success response", statusCode: http.StatusBadRequest, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", request.Method)
				}
				if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
					t.Fatalf("content type = %q, want application/json", contentType)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				if !bytes.Equal(body, []byte(`{"text":"certificate expiring"}`)) {
					t.Fatalf("body = %s", body)
				}
				return &http.Response{
					StatusCode: test.statusCode,
					Body:       io.NopCloser(bytes.NewBufferString("ok")),
					Header:     make(http.Header),
				}, nil
			})}

			reporter := newSlackReport(webhookURL, client)
			err := reporter.SendText("certificate expiring")
			if test.wantError && err == nil {
				t.Fatal("SendText() error = nil, want error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("SendText() error = %v", err)
			}
			if err != nil && bytes.Contains([]byte(err.Error()), []byte("secret-token")) {
				t.Fatalf("SendText() error leaks webhook secret: %v", err)
			}
		})
	}
}

func TestSlackReportRejectsInvalidWebhookURL(t *testing.T) {
	tests := []string{
		"",
		"http://hooks.slack.com/services/T000/B000/secret",
		"https://example.com/services/T000/B000/secret",
		"https://hooks.slack.com/not-a-webhook",
	}

	for _, webhookURL := range tests {
		t.Run(webhookURL, func(t *testing.T) {
			err := newSlackReport(webhookURL, http.DefaultClient).SendText("test")
			if err == nil {
				t.Fatal("SendText() error = nil, want invalid URL error")
			}
			if bytes.Contains([]byte(err.Error()), []byte(webhookURL)) && webhookURL != "" {
				t.Fatalf("SendText() error leaks webhook URL: %v", err)
			}
		})
	}
}
