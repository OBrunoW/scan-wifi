//go:build webview

package main

import (
	"fmt"
	"net/http"
	"os"

	webview "github.com/webview/webview_go"
)

const uiEnabled = true

func runUI(addr string, srv *http.Server, sigCh <-chan os.Signal) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Scan Wi-Fi CSI")
	w.SetSize(1280, 800, webview.HintNone)
	w.Navigate(fmt.Sprintf("http://%s/", addr))

	go func() {
		<-sigCh
		w.Dispatch(func() { w.Terminate() })
		_ = srv.Close()
	}()

	w.Run()
}
