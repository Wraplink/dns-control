package backendregistry

import (
 "context"
 "net/http"
 "net/http/httptest"
 "testing"
)

func TestFetchAgents(t *testing.T) {
 server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.URL.Path != "/api/v1/internal/network/agents/snapshot" || r.Header.Get("X-Network-Control-Key") != "test-control-key" {
   t.Errorf("unexpected request path or authentication")
   w.WriteHeader(http.StatusUnauthorized)
   return
  }
  w.Header().Set("Content-Type", "application/json")
  _, _ = w.Write([]byte(`{"revision":7,"agents":[{"id":"edge-01","status":"offline","capacity":{"sni":2,"route":0},"last_seen_at":"2026-11-08T10:00:00Z"}]}`))
 }))
 defer server.Close()
 client := NewClient(server.URL, "test-control-key")
 snapshot, err := client.FetchAgents(context.Background())
 if err != nil { t.Fatal(err) }
 if snapshot.Revision != 7 || len(snapshot.Agents) != 1 || snapshot.Agents[0].Status != "offline" {
  t.Fatalf("unexpected snapshot: %+v", snapshot)
 }
}

func TestFetchAgentsRejectsMissingRevision(t *testing.T) {
 server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  _, _ = w.Write([]byte(`{"agents":[]}`))
 }))
 defer server.Close()
 _, err := NewClient(server.URL, "key").FetchAgents(context.Background())
 if err == nil { t.Fatal("snapshot without revision must be rejected") }
}

func TestFetchAgentsRejectsMalformedHealth(t *testing.T) {
 for _, body := range []string{
  `{"revision":1,"agents":[{"id":"edge","status":"online"}]}`,
  `{"revision":1,"agents":[{"id":"edge","status":"unknown","last_seen_at":"2026-11-08T10:00:00Z"}]}`,
  `{"revision":1,"agents":[{"id":"edge","status":"online","last_seen_at":"2026-11-08T10:00:00Z"},{"id":"edge","status":"offline","last_seen_at":"2026-11-08T10:00:00Z"}]}`,
 } {
  server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
  _, err := NewClient(server.URL, "key").FetchAgents(context.Background())
  server.Close()
  if err == nil { t.Fatalf("malformed snapshot accepted: %s", body) }
 }
}
