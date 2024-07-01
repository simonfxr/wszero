# Lightweight WebSocket Library for Go

This is a high-performance, lightweight WebSocket library for Go.

## Features

- High performance
- Zero allocations
- Zero copy [writev](https://pkg.go.dev/net#Buffers) path when writing fragments 
- Support for both client and server implementations
- Customizable buffer pool for efficient memory management
- High level API heavily inspired by the popular [gorilla/websocket](https://github.com/gorilla/websocket)
- No third party dependencies
- Tested with [autobahn-testsuite](https://github.com/crossbario/autobahn-testsuite)

## Missing features

- UTF-8 validation of text messages, this can be handled at the application layer
- WebSocket compression

## Installation

To install the library, use `go get`:

```bash
go get github.com/simonfxr/wszero
```

## Quick Start

### Server Example

Here's a simple example of how to create a WebSocket server:

```go
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
```

### Client Example

Here's how to create a WebSocket client:

```go
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
```

## Documentation

For detailed documentation, please refer to the [GoDoc](https://pkg.go.dev/github.com/simonfxr/wszero) page.

## Autobahn Testsuite

The implementation passes all tests in the [Autobahn Testsuite](https://github.com/crossbario/autobahn-testsuite) except those that are related to the explicitly missing features listed above (UTF-8 validation of text messages and message compression).


## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the [MIT License](LICENSE).

# Similar projects

Here is a selection of some existing similar projects

- [gorilla/websocket](https://github.com/gorilla/websocket)
- [gobwas/ws](https://github.com/gobwas/ws)
