package queryreport

import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "log"
 "net/http"
 "strings"
 "sync/atomic"
 "time"

 "github.com/google/uuid"
 mdns "github.com/miekg/dns"
)

type Event struct {
 EventID string `json:"event_id"`
 ObservedAt time.Time `json:"observed_at"`
 Domain string `json:"domain"`
 QType string `json:"qtype"`
 RCode string `json:"rcode"`
 ResolutionMode string `json:"resolution_mode"`
 DurationMS int `json:"duration_ms"`
}
type Reporter struct {
 queue chan Event
 url, key, id string
 client *http.Client
 dropped atomic.Uint64
}
func New(url,key,id string,capacity int)*Reporter{
 if capacity<=0 {capacity=8192}
 return &Reporter{queue:make(chan Event,capacity),url:strings.TrimRight(url,"/")+"/api/v1/internal/network/dns-queries/batch",key:key,id:id,client:&http.Client{Timeout:8*time.Second}}
}
func (r *Reporter) Enqueue(e Event){select{case r.queue<-e:default:r.dropped.Add(1)}}
func (r *Reporter) Dropped()uint64{return r.dropped.Load()}
func (r *Reporter) Run(ctx context.Context){
 ticker:=time.NewTicker(time.Second);defer ticker.Stop()
 pending:=make([]Event,0,100)
 flush:=func(){
  if len(pending)==0{return}
  payload,_:=json.Marshal(struct{ControllerID string `json:"controller_id"`;Events []Event `json:"events"`}{r.id,pending})
  reqCtx,cancel:=context.WithTimeout(ctx,8*time.Second);defer cancel()
  req,err:=http.NewRequestWithContext(reqCtx,http.MethodPost,r.url,bytes.NewReader(payload))
  if err==nil{
   req.Header.Set("Content-Type","application/json");req.Header.Set("X-Network-Control-Key",r.key)
   var resp *http.Response;resp,err=r.client.Do(req)
   if err==nil{resp.Body.Close();if resp.StatusCode<200||resp.StatusCode>=300{err=fmt.Errorf("backend HTTP %d",resp.StatusCode)}}
  }
  if err!=nil{log.Printf("dns query report failed (dropped %d events): %v",len(pending),err);r.dropped.Add(uint64(len(pending)))}
  pending=pending[:0]
 }
 for{
  select{
  case <-ctx.Done():flush();return
  case e:=<-r.queue:pending=append(pending,e);if len(pending)==100{flush()}
  case <-ticker.C:flush()
  }
 }
}
type Handler struct{Next mdns.Handler;Reporter *Reporter}
type writer struct{mdns.ResponseWriter;rcode int;wrote bool}
func (w *writer) WriteMsg(m *mdns.Msg)error{if m!=nil{w.rcode=m.Rcode;w.wrote=true};return w.ResponseWriter.WriteMsg(m)}
func (h Handler) ServeDNS(w mdns.ResponseWriter,req *mdns.Msg){
 start:=time.Now()
 wrapped:=&writer{ResponseWriter:w,rcode:mdns.RcodeServerFailure}
 h.Next.ServeDNS(wrapped,req)
 if req==nil||len(req.Question)==0{return}
 q:=req.Question[0]
 domain:=strings.ToLower(strings.TrimSuffix(q.Name,"."))
 if domain==""||len(domain)>253{return}
 mode:="normal"
 // Resolution mode is added by the DNS policy-aware handler in a follow-up.
 elapsed:=time.Since(start).Milliseconds();if elapsed>600000{elapsed=600000}
 h.Reporter.Enqueue(Event{EventID:uuid.NewString(),ObservedAt:start.UTC(),Domain:domain,QType:mdns.TypeToString[q.Qtype],RCode:mdns.RcodeToString[wrapped.rcode],ResolutionMode:mode,DurationMS:int(elapsed)})
}
