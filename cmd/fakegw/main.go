// Command fakegw runs the emulated FortiGate for manual/integration testing.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http/httptest"

	"myvpn/internal/fakegw"
)

type logReporter struct{}

func (logReporter) Errorf(f string, a ...any) { log.Printf("fakegw: "+f, a...) }
func (logReporter) Error(a ...any)            { log.Print(append([]any{"fakegw: "}, a...)...) }

func main() {
	sso := flag.Int("sso-port", 8020, "puerto SSO local anunciado")
	flag.Parse()
	srv := httptest.NewUnstartedServer(fakegw.Handler(logReporter{}, sso))
	srv.StartTLS()
	sum := sha256.Sum256(srv.Certificate().Raw)
	fmt.Printf("%s %s\n", srv.Listener.Addr(), hex.EncodeToString(sum[:]))
	select {}
}
