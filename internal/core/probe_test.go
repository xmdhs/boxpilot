package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeRequiresAuthenticatedGRPCWeb(t *testing.T) {
	for _, mode := range []string{"valid", "public", "wrong-secret", "html", "truncated", "bad-status", "compressed", "missing-data", "missing-status", "oversize", "redirect", "trailing-data", "header-auth-error"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "redirect" {
					http.Redirect(w, r, "https://example.invalid", 302)
					return
				}
				if mode == "html" {
					w.Write([]byte("<html>not API</html>"))
					return
				}
				w.Header().Set("Content-Type", "application/grpc-web+proto")
				if mode == "wrong-secret" {
					grpcWebReply(w, "16", false)
					return
				}
				if r.Header.Get("Authorization") != "Bearer secret" && mode != "public" {
					if mode == "header-auth-error" {
						w.Header().Set("Grpc-Status", "16")
						return
					}
					grpcWebReply(w, "16", false)
					return
				}
				switch mode {
				case "truncated":
					w.Write([]byte{0, 0, 0, 0, 10})
				case "compressed":
					w.Write([]byte{1, 0, 0, 0, 0})
				case "missing-data":
					grpcWebReply(w, "0", false)
				case "missing-status":
					w.Write([]byte{0, 0, 0, 0, 0})
				case "oversize":
					w.Write(make([]byte, (1<<20)+1))
				case "bad-status":
					grpcWebReply(w, "garbage", true)
				case "trailing-data":
					grpcWebReply(w, "0", true)
					w.Write([]byte{0, 0, 0, 0, 0})
				default:
					grpcWebReply(w, "0", true)
				}
			}))
			defer server.Close()
			err := probeAPI(context.Background(), server.URL, "secret")
			if mode == "valid" || mode == "header-auth-error" {
				if err != nil || calls != 3 {
					t.Fatalf("probe: %v calls=%d", err, calls)
				}
			} else if err == nil {
				t.Fatal("accepted unsafe/malformed readiness response")
			}
		})
	}
}
