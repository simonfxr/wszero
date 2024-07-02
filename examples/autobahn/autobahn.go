package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/simonfxr/wszero"
)

func main() {
	log.SetFlags(0)

	server := &http.Server{}
	flag.StringVar(&server.Addr, "listen", ":9001", "addr to listen")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		const timeout = 5 * time.Second
		log.Printf("signal received; shutting down with %s timeout", timeout)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()

	http.HandleFunc("/simple", wsHandler)
	http.HandleFunc("/frames1", frameHandler(true))
	http.HandleFunc("/frames2", frameHandler(false))

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		cancel()
		log.Printf("server %q error: %v", server.Addr, err)
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := (&wszero.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %s", err)
		return
	}
	defer conn.Close()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("read message error: %s", err)
			return
		}
		err = conn.WriteMessage(mt, data)
		conn.BufferPool().PutBuffer(data)
		if err != nil {
			log.Printf("write message error: %s", err)
			return
		}
	}
}

func frameHandler(final bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&wszero.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade error: %s", err)
			return
		}
		defer conn.Close()

		writer := &wszero.FrameWriter{}
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				log.Printf("read message error: %s", err)
				return
			}
			writer.Reset(conn, mt)
			if final {
				writer.Final()
			}
			err = writer.Write(data)
			if err != nil {
				log.Printf("write message error: %s", err)
				return
			}
			err = writer.Close()
			if err != nil {
				log.Printf("write message error: %s", err)
				return
			}
		}
	}
}
