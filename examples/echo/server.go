//go:build ignore
// +build ignore

package main

import (
	"log"
	"net/http"

	"github.com/simonfxr/wszero"
)

func main() {
	upgrader := &wszero.Upgrader{}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Upgrade error:", err)
			return
		}
		defer conn.Close()

		for {
			messageType, p, err := conn.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				return
			}
			if err := conn.WriteMessage(messageType, p); err != nil {
				log.Println("Write error:", err)
				return
			}
			// (optional) return buffer to the pool
			conn.BufferPool().PutBuffer(p)
		}
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}
