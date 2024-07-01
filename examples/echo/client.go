//go:build ignore
// +build ignore

package main

import (
	"context"
	"github.com/simonfxr/wszero"
	"log"
)

func main() {
	dialer := wszero.DefaultDialer

	conn, _, err := dialer.DialContext(context.Background(), "ws://localhost:8080/ws", nil)
	if err != nil {
		log.Fatal("Dial error:", err)
	}
	defer conn.Close()

	err = conn.WriteMessageString(wszero.TextMessage, "Hello, WebSocket!")
	if err != nil {
		log.Fatal("Write error:", err)
	}

	_, message, err := conn.ReadMessage()
	if err != nil {
		log.Fatal("Read error:", err)
	}

	log.Printf("Received: %s", message)
}
