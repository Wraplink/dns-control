package alertreport

import (
 "bytes"
 "context"
 "encoding/json"
 "net/http"
 "os"
 "strings"
 "time"
)

type Reporter struct {
 URL string
 Key string
 Service string
 Environment string
 Client *http.Client
}
func FromEnv(service string) Reporter {
 return Reporter{
  URL:strings.TrimRight(os.Getenv("WRAPLINK_ALERT_API_URL"),"/"),
  Key:os.Getenv("WRAPLINK_ALERT_INGEST_KEY"),
  Service:service,
  Environment:os.Getenv("WRAPLINK_ENVIRONMENT"),
  Client:&http.Client{Timeout:4*time.Second},
 }
}
func (r Reporter) Send(code, severity, summary string) error {
 if r.URL==""||r.Key=="" {return nil}
 body,_:=json.Marshal(map[string]string{
  "service":r.Service,"severity":severity,"code":code,
  "fingerprint":code,"summary":summary,"environment":r.Environment,
 })
 ctx,cancel:=context.WithTimeout(context.Background(),4*time.Second)
 defer cancel()
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,r.URL+"/api/v1/internal/alerts",bytes.NewReader(body))
 if err!=nil{return err}
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("X-Wraplink-Alert-Key",r.Key)
 resp,err:=r.Client.Do(req)
 if err!=nil{return err}
 defer resp.Body.Close()
 if resp.StatusCode!=http.StatusAccepted {return &deliveryError{Status:resp.StatusCode}}
 return nil
}
type deliveryError struct{Status int}
func(e *deliveryError) Error() string{return "alert intake rejected request"}
