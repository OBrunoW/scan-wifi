//go:build !webview

package main

import (
	"log"
	"net/http"
	"os"
)

const uiEnabled = false

func runUI(addr string, srv *http.Server, sigCh <-chan os.Signal) {
	log.Printf("webview desabilitado; abra http://%s/", addr)
	<-sigCh
	_ = srv.Close()
}
