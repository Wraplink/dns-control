package domain

import (
 "context"
 "encoding/json"
 "fmt"
 "net/http"
 "strings"
 "time"
)

type Snapshot struct {
 Revision int64 `json:"revision"`
 Domains []Rule `json:"domains"`
}
type Client struct {
 url string
 key string
 http *http.Client
}
func NewClient(url,key string)*Client{
 return &Client{url:strings.TrimRight(strings.TrimSpace(url),"/"),key:key,http:&http.Client{Timeout:10*time.Second}}
}
func(c *Client) Fetch(ctx context.Context)(Snapshot,error){
 req,err:=http.NewRequestWithContext(ctx,http.MethodGet,c.url+"/api/v1/internal/network/domains",nil)
 if err!=nil{return Snapshot{},err}
 req.Header.Set("X-Network-Control-Key",c.key)
 response,err:=c.http.Do(req)
 if err!=nil{return Snapshot{},err}
 defer response.Body.Close()
 if response.StatusCode!=http.StatusOK{return Snapshot{},fmt.Errorf("policy snapshot returned HTTP %d",response.StatusCode)}
 var result Snapshot
 if err:=json.NewDecoder(response.Body).Decode(&result);err!=nil{return Snapshot{},err}
 if result.Revision<=0{return Snapshot{},fmt.Errorf("invalid policy revision")}
 return result,nil
}
