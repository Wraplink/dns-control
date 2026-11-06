package alertreport

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"
)

func TestSendAuthenticatedAlert(t *testing.T) {
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/api/v1/internal/alerts" {t.Errorf("path: %s",r.URL.Path)}
  if r.Header.Get("X-Wraplink-Alert-Key")!="test-key" {t.Error("missing authentication")}
  var body map[string]string
  if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil {t.Error(err)}
  if body["service"]!="edge-agent" && body["service"]!="dns-control" {t.Errorf("service: %s",body["service"])}
  if body["code"]!="health_failure" {t.Errorf("code: %s",body["code"])}
  w.WriteHeader(http.StatusAccepted)
 }))
 defer server.Close()
 reporter:=Reporter{URL:server.URL,Key:"test-key",Service:"edge-agent",Environment:"test",Client:server.Client()}
 if err:=reporter.Send("health_failure","error","Health check failed");err!=nil {t.Fatal(err)}
}
func TestDisabledReporter(t *testing.T) {
 if err:=(Reporter{}).Send("health_failure","error","Health check failed");err!=nil {t.Fatal(err)}
}
