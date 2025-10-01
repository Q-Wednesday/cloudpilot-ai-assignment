package main

import (
	"net/http"

	"k8s.io/klog/v2"

	"github.com/Q-Wednesday/cloudpilot-ai-assignment/101_distributed_scheduling/webhook"
)

func main() {
	http.HandleFunc("/mutate", webhook.ServeMutate)

	klog.Info("Starting webhook server on :8443...")
	server := &http.Server{
		Addr: ":8443",
	}
	if err := server.ListenAndServeTLS("/tls/tls.crt", "/tls/tls.key"); err != nil {
		panic(err)
	}
}
